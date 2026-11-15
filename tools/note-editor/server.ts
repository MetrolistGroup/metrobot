import { chmodSync, closeSync, existsSync, fsyncSync, openSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { timingSafeEqual } from "node:crypto";
import { resolve } from "node:path";

export type AIText = { title: string; category: string; language: "go" | "markdown"; source: string; deployment: string; kind: "memory" | "prompt" | "source" | "reference"; max_bytes: number; readonly?: boolean; binding: { type: "file" | "setting" | "source" | "none"; target: string; exists?: boolean } };
export type Note = { name: string; short_desc: string; content: string; markup_enabled: number; document?: AIText };
type Snapshot = { source: string; fetched_at: string; notes: Note[] };
type Options = { dataDir: string; token: string; formatter: string; hosts: string[]; assets?: Record<string, Blob | string> };
const MAX_CONTENT = 512_000;
const hash = (value: string) => new Bun.CryptoHasher("sha256").update(value).digest("hex");
export const revision = (note: Note) => hash(JSON.stringify([note.name, note.short_desc, note.content, note.markup_enabled]));
const equal = (a: string, b: string) => {
  const left = Buffer.from(a), right = Buffer.from(b);
  return left.length === right.length && timingSafeEqual(left, right);
};

export async function formatNote(formatter: string, content: string, literal = false, mode = "") {
  const child = Bun.spawn([formatter], { stdin: "pipe", stdout: "pipe", stderr: "pipe" });
  child.stdin.write(JSON.stringify({ content, literal, mode }));
  child.stdin.end();
  const [output, errors, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]);
  if (code !== 0) throw new Error(`Note formatter failed: ${errors.slice(0, 200)}`);
  return JSON.parse(output);
}

export function createHandler(options: Options) {
  const draftsPath = resolve(options.dataDir, "drafts.json");
  const snapshot: Snapshot = JSON.parse(readFileSync(draftsPath, "utf8"));
  const baseline: Snapshot = JSON.parse(readFileSync(resolve(options.dataDir, "baseline.json"), "utf8"));
  const stores = [{ path: draftsPath, snapshot, baseline }];
  const aiPath = resolve(options.dataDir, "ai-drafts.json");
  if (existsSync(aiPath)) stores.push({ path: aiPath, snapshot: JSON.parse(readFileSync(aiPath, "utf8")), baseline: JSON.parse(readFileSync(resolve(options.dataDir, "ai-baseline.json"), "utf8")) });
  const allNotes = () => stores.flatMap(store => store.snapshot.notes);
  const original = new Map(stores.flatMap(store => store.baseline.notes.map(note => [note.name, revision(note)] as const)));
  const session = hash(`${options.token}:session`);
  const headers = {
    "Cache-Control": "no-store",
    "X-Content-Type-Options": "nosniff",
    "Referrer-Policy": "no-referrer",
    "Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; media-src https:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
  };
  const json = (data: unknown, status = 200) => Response.json(data, { status, headers });
  const publicNote = (note: Note) => ({ ...note, revision: revision(note), changed: original.get(note.name) !== revision(note) });
  const validContent = (value: unknown): value is string => typeof value === "string" && value.length <= MAX_CONTENT;

  return async (request: Request): Promise<Response> => {
    const url = new URL(request.url);
    if (!options.hosts.includes(url.host)) return json({ error: "Unrecognized host" }, 403);
    const origin = request.headers.get("Origin");
    if ((origin && origin !== url.origin) || (!["GET", "HEAD"].includes(request.method) && origin !== url.origin)) {
      return json({ error: "Same-origin requests only" }, 403);
    }
    if (Number(request.headers.get("Content-Length")) > 1_048_576) return json({ error: "Request too large" }, 413);
    try {
      if (url.pathname === "/session" && request.method === "POST") {
        const body = await request.json();
        if (typeof body.token !== "string" || !equal(body.token, options.token)) return json({ error: "Invalid access key" }, 401);
        const response = json({ ok: true });
        response.headers.set("Set-Cookie", `notes_session=${session}; HttpOnly; SameSite=Strict; Path=/; Max-Age=86400`);
        return response;
      }
      if (request.method === "GET" && options.assets?.[url.pathname] !== undefined) {
        const type = url.pathname.endsWith(".js") ? "text/javascript" : url.pathname.endsWith(".css") ? "text/css" : url.pathname === "/reference" ? "text/plain; charset=utf-8" : "text/html; charset=utf-8";
        return new Response(options.assets[url.pathname], { headers: { ...headers, "Content-Type": type } });
      }
      const cookie = (request.headers.get("Cookie") ?? "").split(";").map(part => part.trim()).find(part => part.startsWith("notes_session="))?.slice("notes_session=".length) ?? "";
      if (!equal(cookie, session)) return json({ error: "Open your private access link to sign in" }, 401);
      if (url.pathname === "/api/notes" && request.method === "GET") {
        return json({ source: snapshot.source, fetched_at: snapshot.fetched_at, notes: allNotes().map(publicNote) });
      }
      if (url.pathname === "/api/export" && request.method === "GET") {
        const response = json({ ...snapshot, ...(stores[1] ? { ai: stores[1].snapshot } : {}) });
        response.headers.set("Content-Disposition", 'attachment; filename="metrobot-notes-and-ai-text.json"');
        return response;
      }
      if (url.pathname === "/api/preview" && request.method === "POST") {
        const body = await request.json();
        if (!validContent(body.content) || typeof body.literal !== "boolean") return json({ error: "Invalid preview request" }, 400);
        const document = body.document_name === undefined ? undefined : allNotes().find(note => note.name === body.document_name)?.document;
        if (body.document_name !== undefined && !document) return json({ error: "AI document not found" }, 404);
        const result = await formatNote(options.formatter, body.content, body.literal, document?.language);
        return json(result, result.error ? 422 : 200);
      }
      if (url.pathname.startsWith("/api/notes/") && request.method === "PUT") {
        const name = decodeURIComponent(url.pathname.slice("/api/notes/".length));
        const store = stores.find(store => store.snapshot.notes.some(note => note.name === name));
        if (!store) return json({ error: "Document not found" }, 404);
        const { snapshot, path: draftsPath } = store;
        const index = snapshot.notes.findIndex(note => note.name === name);
        const document = snapshot.notes[index].document;
        if (document?.readonly) return json({ error: "This input map is reference-only. Edit the actual prompts, skills or memory instead." }, 403);
        const body = await request.json();
        if (!validContent(body.content) || typeof body.short_desc !== "string" || typeof body.revision !== "string") return json({ error: "Invalid note fields" }, 400);
        const description = document ? snapshot.notes[index].short_desc : body.short_desc.trim();
        // Blank descriptions intentionally opt notes out of AI routing, including existing legacy rows.
        if (/[\r\n]/.test(description) || [...description].length > 160) return json({ error: "Description must be one line and at most 160 characters" }, 422);
        if (body.revision !== revision(snapshot.notes[index])) return json({ error: "This note was saved in another tab. Keep your text, then reload before saving." }, 409);
        if (body.content === snapshot.notes[index].content && description === snapshot.notes[index].short_desc) return json(publicNote(snapshot.notes[index]));
        let proposed = body.content;
        if (document) {
          if (!proposed.trim()) return json({ error: "AI text cannot be blank" }, 422);
          if (document.kind === "memory") proposed = proposed.trim() + "\n";
          if (Buffer.byteLength(proposed, "utf8") > document.max_bytes) return json({ error: `This document cannot exceed ${document.max_bytes} UTF-8 bytes` }, 422);
        }
        const result = await formatNote(options.formatter, proposed, false, document?.language);
        if (result.error) return json({ error: result.error }, 422);
        // Check again after parsing: another request may have saved while the helper was running.
        if (body.revision !== revision(snapshot.notes[index])) return json({ error: "Another save won the race. Reload before saving." }, 409);
        const updated: Note = { ...snapshot.notes[index], name, short_desc: description, content: result.content, markup_enabled: document ? 0 : 1 };
        const next: Snapshot = { ...snapshot, notes: snapshot.notes.map((note, i) => i === index ? updated : note) };
        const temporary = `${draftsPath}.tmp`;
        writeFileSync(temporary, JSON.stringify(next, null, 2) + "\n", { mode: 0o600 });
        const fd = openSync(temporary, "r");
        try { fsyncSync(fd); } finally { closeSync(fd); }
        renameSync(temporary, draftsPath);
        snapshot.notes = next.notes;
        return json(publicNote(updated));
      }
      return json({ error: "Not found" }, 404);
    } catch (error) {
      if (error instanceof SyntaxError || error instanceof URIError) return json({ error: "Invalid JSON or URL" }, 400);
      console.error(error);
      return json({ error: "Could not complete the request. Your stored draft has not been replaced." }, 500);
    }
  };
}

if (import.meta.main) {
  const dataDir = process.env.NOTES_DATA_DIR;
  if (!dataDir) throw new Error("Set NOTES_DATA_DIR to the private directory containing baseline.json and drafts.json");
  const host = process.env.HOST ?? "127.0.0.1";
  const port = Number(process.env.PORT ?? 8084);
  const formatter = resolve(dataDir, "note-format");
  const build = Bun.spawn(["go", "build", "-o", formatter, "./tools/note-editor/format"], { cwd: resolve(import.meta.dir, "../.."), stdout: "inherit", stderr: "inherit" });
  if (await build.exited !== 0) throw new Error("Could not build the existing note parser helper");
  const bundle = await Bun.build({ entrypoints: [resolve(import.meta.dir, "app.ts")], target: "browser", minify: true });
  if (!bundle.success) throw new AggregateError(bundle.logs, "Browser build failed");
  const tokenPath = resolve(dataDir, "access-token");
  const tokenFile = Bun.file(tokenPath);
  const token = await tokenFile.exists() ? (await tokenFile.text()).trim() : Buffer.from(crypto.getRandomValues(new Uint8Array(32))).toString("hex");
  writeFileSync(tokenPath, token + "\n", { mode: 0o600 });
  chmodSync(tokenPath, 0o600);
  const handler = createHandler({ dataDir, token, formatter, hosts: [`${host}:${port}`], assets: {
    "/": Bun.file(resolve(import.meta.dir, "index.html")),
    "/app.js": bundle.outputs[0],
    "/style.css": Bun.file(resolve(import.meta.dir, "style.css")),
    "/reference": Bun.file(resolve(import.meta.dir, "../../docs/notes.md")),
  } });
  const server = Bun.serve({ hostname: host, port, fetch: handler, maxRequestBodySize: 1_048_576 });
  console.log(`Note editor ready: http://${host}:${server.port}/`);
  console.log(`Private access key is in ${tokenPath}. Production is never contacted by this editor.`);
}
