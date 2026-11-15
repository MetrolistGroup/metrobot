import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createHandler, formatNote, revision, type Note } from "./server";
import { sourceCategory } from "./import-ai";

const root = mkdtempSync(join(tmpdir(), "metrobot-note-editor-test-"));
const formatter = join(root, "note-format");
const origin = "http://127.0.0.1:8084";
const token = "a".repeat(64);
const plain: Note = { name: "sample", short_desc: "Routing description", content: "# Original\nBody", markup_enabled: 1 };
const legacy: Note = { name: "legacy", short_desc: "", content: "```components-v2\nnot JSON\n```", markup_enabled: 0 };
const source: Note = { name: "ai/source/prompts.go", short_desc: "Compiled prompt source", content: 'package example\nconst prompt = "Original prompt"\n', markup_enabled: 0, document: { title: "Prompts", category: "prompts", language: "go", source: "prompts.go", deployment: "Rebuild required", kind: "source", max_bytes: 512_000, binding: { type: "source", target: "prompts.go" } } };
const memory: Note = { name: "ai/memory.md", short_desc: "Shared memory", content: "# Memory\n", markup_enabled: 0, document: { title: "Memory", category: "runtime", language: "markdown", source: "garmin-memory.md", deployment: "Runtime file", kind: "memory", max_bytes: 16_384, binding: { type: "file", target: "/opt/metrobot/garmin-memory.md" } } };
const inputMap: Note = { ...memory, name: "ai/AGENTS.md", document: { ...memory.document!, kind: "reference", readonly: true, binding: { type: "none", target: "" } } };

beforeAll(async () => {
  const child = Bun.spawn(["go", "build", "-o", formatter, "./tools/note-editor/format"], { cwd: resolve(import.meta.dir, "../.."), stderr: "inherit" });
  expect(await child.exited).toBe(0);
}, 60_000);
afterAll(() => rmSync(root, { recursive: true, force: true }));

async function fixture() {
  const dataDir = mkdtempSync(join(root, "drafts-"));
  const snapshot = JSON.stringify({ source: "test fixture", fetched_at: "2026-01-01", notes: [plain, legacy] });
  for (const file of ["baseline.json", "drafts.json"]) writeFileSync(join(dataDir, file), snapshot, { mode: 0o600 });
  const ai = JSON.stringify({ source: "deployed revision", fetched_at: "2026-01-01", notes: [source, memory, inputMap] });
  for (const file of ["ai-baseline.json", "ai-drafts.json"]) writeFileSync(join(dataDir, file), ai, { mode: 0o600 });
  const handler = createHandler({ dataDir, token, formatter, hosts: ["127.0.0.1:8084"], assets: { "/": "<html></html>" } });
  let cookie = "";
  const request = (path: string, body?: unknown, method = "POST", headers: Record<string, string> = {}) => handler(new Request(origin + path, {
    method: body === undefined ? "GET" : method,
    headers: { Cookie: cookie, Origin: origin, "Content-Type": "application/json", ...headers },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  }));
  const login = await request("/session", { token });
  cookie = login.headers.get("Set-Cookie")!.split(";")[0];
  return { request, handler, dataDir };
}

test("private notes require authentication; write requests require the exact origin", async () => {
  const { handler, request } = await fixture();
  expect((await handler(new Request(origin + "/api/notes"))).status).toBe(401);
  expect((await request("/session", { token: "é".repeat(64) })).status).toBe(401);
  expect((await request("/session", { token }, "POST", { Origin: "https://evil.invalid" })).status).toBe(403);
  expect((await handler(new Request("http://evil.invalid:8084/"))).status).toBe(403);
  const page = await request("/");
  expect(page.headers.get("Content-Security-Policy")).toContain("frame-ancestors 'none'");
  expect(page.headers.get("Cache-Control")).toBe("no-store");
});

test("Components V2 uses the real parser, including nested examples and validation", async () => {
  const source = 'Before\n```components-v2\n{"components":[{"type":"text","content":"# Hello"},{"type":"separator"},{"type":"buttons","buttons":[{"label":"Source","url":"https://github.com/MetrolistGroup/Metrolist"}]}]}\n```\nAfter';
  const result = await formatNote(formatter, source);
  expect(result.rendered.Layout.components[0].Content).toBe("# Hello");
  expect(result.rendered.Layout.components[2].Buttons[0].label).toBe("Source");
  expect(result.rendered.Before).toContain("Before");
  expect(result.rendered.After).toContain("After");
  expect((await formatNote(formatter, source.replace("https://", "javascript:"))).error).toBeDefined();
  const literal = await formatNote(formatter, '````markdown\n' + source + '\n````');
  expect(literal.rendered.Layout).toBeNull();
  expect((await formatNote(formatter, '```faq\n# Help\n## Where?\nHere.\n```')).rendered.FAQ.Items[0].Answer).toBe("Here.");
});

test("validated edits persist locally without modifying the baseline; stale tabs cannot overwrite", async () => {
  const { request, dataDir } = await fixture();
  const baseline = readFileSync(join(dataDir, "baseline.json"), "utf8");
  const body = { content: "# Edited\nSaved body", short_desc: "", revision: revision(plain) };
  const response = await request("/api/notes/sample", body, "PUT");
  expect(response.status).toBe(200);
  expect((await response.json()).changed).toBe(true);
  expect(JSON.parse(readFileSync(join(dataDir, "drafts.json"), "utf8")).notes[0].content).toBe(body.content);
  expect(readFileSync(join(dataDir, "baseline.json"), "utf8")).toBe(baseline);
  expect((await request("/api/notes/sample", body, "PUT")).status).toBe(409);
  expect((await request("/api/notes/missing", body, "PUT")).status).toBe(404);
});

test("invalid content never replaces drafts; unchanged legacy notes stay literal", async () => {
  const { request, dataDir } = await fixture();
  const original = readFileSync(join(dataDir, "drafts.json"), "utf8");
  expect((await request("/api/notes/sample", { ...plain, content: "```components-v2\nnot JSON\n```", revision: revision(plain) }, "PUT")).status).toBe(422);
  expect((await request("/api/notes/sample", { ...plain, short_desc: "x".repeat(161), revision: revision(plain) }, "PUT")).status).toBe(422);
  expect(readFileSync(join(dataDir, "drafts.json"), "utf8")).toBe(original);
  const noOp = await request("/api/notes/legacy", { ...legacy, revision: revision(legacy) }, "PUT");
  expect((await noOp.json()).markup_enabled).toBe(0);
  expect(readFileSync(join(dataDir, "drafts.json"), "utf8")).toBe(original);
  const preview = await request("/api/preview", { content: legacy.content, literal: true });
  expect((await preview.json()).rendered.Layout).toBeNull();
});

test("simultaneous saves reject the loser instead of losing an edit", async () => {
  const { request } = await fixture();
  const responses = await Promise.all(["first", "second"].map(content => request("/api/notes/sample", { content, short_desc: "route", revision: revision(plain) }, "PUT")));
  expect(responses.map(response => response.status).sort()).toEqual([200, 409]);
});

test("AI source edits validate Go syntax, persist separately and preserve their deployment binding", async () => {
  const { request, dataDir } = await fixture();
  const notesBefore = readFileSync(join(dataDir, "drafts.json"), "utf8");
  const baselineBefore = readFileSync(join(dataDir, "ai-baseline.json"), "utf8");
  const path = "/api/notes/" + encodeURIComponent(source.name);
  expect((await request(path, { ...source, content: 'package broken\nconst prompt = "unclosed', revision: revision(source) }, "PUT")).status).toBe(422);
  const content = 'package example\nconst prompt = "New prompt"\n';
  const response = await request(path, { content, short_desc: "do not alter metadata", revision: revision(source), document: { readonly: false, binding: { target: "/etc/passwd" } } }, "PUT");
  expect(response.status).toBe(200);
  const saved = await response.json();
  expect(saved.content).toBe(content);
  expect(saved.document.binding.target).toBe("prompts.go");
  expect(saved.short_desc).toBe(source.short_desc);
  expect(saved.markup_enabled).toBe(0);
  expect(saved.changed).toBe(true);
  expect(JSON.parse(readFileSync(join(dataDir, "ai-drafts.json"), "utf8")).notes[0].content).toBe(content);
  expect(readFileSync(join(dataDir, "drafts.json"), "utf8")).toBe(notesBefore);
  expect(readFileSync(join(dataDir, "ai-baseline.json"), "utf8")).toBe(baselineBefore);
  const preview = await request("/api/preview", { content, literal: false, document_name: source.name });
  expect((await preview.json()).literals[0].content).toBe("New prompt");
});

test("AI Markdown remains literal, memory respects byte limits, reference maps cannot be saved", async () => {
  const { request } = await fixture();
  const content = '```components-v2\nnot a Discord directive here\n```';
  const preview = await request("/api/preview", { content, literal: false, document_name: memory.name });
  expect(preview.status).toBe(200);
  expect((await preview.json()).rendered.Text).toBe(content);
  const path = "/api/notes/" + encodeURIComponent(memory.name);
  const tooLarge = await request(path, { content: "😀".repeat(4096), short_desc: memory.short_desc, revision: revision(memory) }, "PUT");
  expect(tooLarge.status).toBe(422);
  const save = await request(path, { content: "  # Updated  ", short_desc: memory.short_desc, revision: revision(memory) }, "PUT");
  expect((await save.json()).content).toBe("# Updated\n");
  expect((await request("/api/notes/" + encodeURIComponent(inputMap.name), { ...inputMap, content: "overwrite", revision: revision(inputMap) }, "PUT")).status).toBe(403);
  const exported = await (await request("/api/export")).json();
  expect(exported.notes).toHaveLength(2);
  expect(exported.ai.notes).toHaveLength(3);
});

test("inventory covers every direct model call and all embedded guides without importing secrets or tests", async () => {
  const repo = resolve(import.meta.dir, "../..");
  const result = Bun.spawnSync(["git", "grep", "-l", "-E", "(garminAI\\.Complete|garminDecisions\\.Decide|c\\.Complete)\\(", "--", "*.go", ":(exclude)*_test.go"], { cwd: repo });
  expect(result.exitCode).toBe(0);
  for (const path of result.stdout.toString().trim().split("\n")) expect(sourceCategory(path)).toBeDefined();
  for await (const path of new Bun.Glob("discord/garmin_skills/*.md").scan({ cwd: repo })) expect(sourceCategory(path)).toBe("skills");
  for (const path of ["config.json", ".env", "discord/garmin_agent_test.go", "github/assistant_test.go"]) expect(sourceCategory(path)).toBeUndefined();
});
