import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { formatNote, type AIText, type Note } from "./server";

export function sourceCategory(path: string): string | undefined {
  if (path.endsWith("_test.go")) return;
  if (/^discord\/garmin_skills\/[^/]+\.md$/.test(path)) return "skills";
  if (["cmd/garmin_prompt.go", "cmd/garmin_reply.go", "cmd/chat_decisions.go", "discord/garmin_support.go", "discord/support_decisions.go", "discord/support_answer.go", "discord/support_excerpt.go", "discord/support_learning.go", "discord/ocr.go", "discord/ocr_image.go"].includes(path)) return "prompts";
  if (/^(github|firecrawl|gsmarena|nanoreview|technicalcity|internal\/notefmt)\/[^/]+\.go$/.test(path) || ["cmd/calculator.go", "cmd/notes.go"].includes(path)) return "tools";
  if (/^discord\/(garmin[^/]*|support[^/]*)\.go$/.test(path) || /^cmd\/(garmin[^/]*|ai|decisions|openrouter)\.go$/.test(path) || path === "discord/bot.go") return "runtime";
}

const titles: Record<string, string> = {
  "cmd/garmin_prompt.go": "Chat, #general, safety & style",
  "cmd/garmin_reply.go": "Embed format instructions",
  "cmd/chat_decisions.go": "Qwen decision prompts & schema",
  "discord/garmin_support.go": "Support & legacy triage prompts",
  "discord/support_decisions.go": "Support routing & criteria",
  "discord/support_answer.go": "Evidence checker & retry hints",
  "discord/support_excerpt.go": "Note section selector",
  "discord/support_learning.go": "Confirmed-fix learning prompt",
  "discord/ocr.go": "OCR & translation prompt",
  "discord/ocr_image.go": "OCR in-place overlay prompt",
  "discord/garmin_agent.go": "Runtime instructions & retries",
  "discord/garmin_tools.go": "Every tool description & schema",
  "discord/garmin_profile.go": "Discord context & channel text",
  "discord/garmin_context.go": "Conversation & omission text",
  "discord/garmin_prompt.go": "System prompt assembly",
};

if (import.meta.main) {
  const dataDir = resolve(process.argv[2] ?? process.env.NOTES_DATA_DIR ?? "");
  if (!process.argv[2] && !process.env.NOTES_DATA_DIR) throw new Error("Pass the private snapshot directory");
  if (["ai-baseline.json", "ai-drafts.json"].some(name => existsSync(resolve(dataDir, name)))) throw new Error("AI snapshots already exist; refusing to replace any baseline or drafts");
  const runtime = JSON.parse(readFileSync(resolve(dataDir, "ai-runtime.json"), "utf8"));
  const repo = resolve(import.meta.dir, "../..");
  const git = (...args: string[]) => {
    const result = Bun.spawnSync(["git", ...args], { cwd: repo });
    if (result.exitCode !== 0) throw new Error(result.stderr.toString());
    return result.stdout.toString();
  };
  if (!/^[a-f0-9]{40}$/.test(runtime.git_revision) || git("rev-parse", "HEAD").trim() !== runtime.git_revision || runtime.modified) throw new Error("Deployed source does not exactly match HEAD; fetch its actual source before importing");
  const notes: Note[] = [];
  const add = (name: string, content: string, description: string, document: AIText) => notes.push({ name, short_desc: description, content, markup_enabled: 0, document });
  add("ai/AGENTS.md", await Bun.file(resolve(import.meta.dir, "AI-INPUTS.md")).text(), "Reference inventory only. Metrobot does not load an AGENTS.md file.", {
    title: "AGENTS.md / complete input map", category: "map", language: "markdown", source: "tools/note-editor/AI-INPUTS.md", deployment: "Reference only; not sent to the AI and never uploaded as bot instructions.", kind: "reference", max_bytes: 512_000, readonly: true, binding: { type: "none", target: "" },
  });
  const builtIn = await formatNote(resolve(dataDir, "note-format"), "", false, "base-prompt");
  if (!builtIn.content) throw new Error("Could not export the effective default prompt");
  add("ai/chat-system-prompt.md", runtime.prompt_override ?? builtIn.content, "Effective ordinary-chat base prompt. #general, support, routing and OCR use separate built-in instructions.", {
    title: "Chat system prompt (effective)", category: "prompts", language: "markdown", source: "SQLite settings.garmin_base_system_prompt" + (runtime.prompt_override === null ? " (absent; built-in fallback shown)" : " (active override)"), deployment: "Changed text becomes the database prompt override. New ordinary-chat requests read it without a restart.", kind: "prompt", max_bytes: 65_536, binding: { type: "setting", target: "garmin_base_system_prompt", exists: runtime.prompt_override !== null },
  });
  add("ai/shared-memory.md", runtime.memory, "Live shared project memory for ordinary chat. Lower-priority data, not instructions; support does not receive this file.", {
    title: "Shared memory (live VPS)", category: "runtime", language: "markdown", source: runtime.memory_path, deployment: "Writes back only to the configured memory file after confirmation. Maximum 16 KiB including the trailing newline.", kind: "memory", max_bytes: 16_384, binding: { type: "file", target: runtime.memory_path, exists: true },
  });
  const paths = git("ls-tree", "-r", "--name-only", runtime.git_revision).trim().split("\n");
  for (const path of paths) {
    const category = sourceCategory(path);
    if (!category) continue;
    const markdown = path.endsWith(".md");
    add("ai/source/" + path, git("show", `${runtime.git_revision}:${path}`), markdown ? "Embedded authored Markdown. Used as the project reference, support core or an on-demand guide." : "Exact deployed source. Edit its strings in place; the preview decodes every literal, including schemas, labels and incidental code strings.", {
      title: titles[path] ?? (markdown ? path.split("/").at(-1)! : path), category, language: markdown ? "markdown" : "go", source: `${path} @ ${runtime.git_revision.slice(0, 12)}`, deployment: "Compiled into Metrobot. Changes require source integration, build/tests and a binary redeployment; uploading a loose file will not activate them.", kind: "source", max_bytes: 512_000, binding: { type: "source", target: path, exists: true },
    });
  }
  if (new Set(notes.map(note => note.name)).size !== notes.length) throw new Error("Duplicate AI document IDs");
  const snapshot = { source: runtime.source, fetched_at: runtime.fetched_at, git_revision: runtime.git_revision, binary_sha256: runtime.binary_sha256, models: runtime.models, agent_files: runtime.agent_files, notes };
  const serialized = JSON.stringify(snapshot, null, 2) + "\n";
  writeFileSync(resolve(dataDir, "ai-baseline.json"), serialized, { flag: "wx", mode: 0o400 });
  writeFileSync(resolve(dataDir, "ai-drafts.json"), serialized, { flag: "wx", mode: 0o600 });
  console.log(`Imported ${notes.length} AI documents at deployed revision ${runtime.git_revision}. Existing note drafts were not touched.`);
  for (const category of ["map", "prompts", "skills", "runtime", "tools"]) console.log(`${category}: ${notes.filter(note => note.document!.category === category).length}`);
}
