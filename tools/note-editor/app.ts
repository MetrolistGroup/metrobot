import { basicSetup, EditorView } from "codemirror";
import { markdown, markdownLanguage } from "@codemirror/lang-markdown";
import { jsonLanguage } from "@codemirror/lang-json";
import { oneDark } from "@codemirror/theme-one-dark";
import { go } from "@codemirror/lang-go";
import { marked } from "marked";
import DOMPurify from "dompurify";
import type { Note } from "./server";

type Draft = Note & { revision: string; changed: boolean };
const $ = <T extends HTMLElement = HTMLElement>(id: string) => document.getElementById(id) as T;
const description = $<HTMLInputElement>("description");
const saveButton = $<HTMLButtonElement>("save");
let notes: Draft[] = [];
let current: Draft | undefined;
let editor: EditorView;
let suppress = false;
let saving = false;
let storagePrefix = "";
let readable = false;
let latestPreview: any;
let latestLiterals: { line: number; content: string }[] = [];
let previewVersion = 0;
let previewTimer: ReturnType<typeof setTimeout>;
let previewAbort: AbortController | undefined;

async function api(path: string, body?: unknown, method = "POST", signal?: AbortSignal) {
  const response = await fetch(path, body === undefined ? { signal } : {
    method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body), signal,
  });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || `Request failed (${response.status})`);
  return result;
}
const content = () => editor?.state.doc.toString() ?? "";
const dirty = () => !!current && (content() !== current.content || description.value !== current.short_desc);
function notice(text = "") { $("notice").textContent = text; $("notice").hidden = !text; }
function recoveryKey() { return storagePrefix + current!.name; }
function remember() {
  if (!current) return;
  try {
    if (dirty()) localStorage.setItem(recoveryKey(), JSON.stringify({ revision: current.revision, content: content(), short_desc: description.value }));
    else localStorage.removeItem(recoveryKey());
  } catch { notice("Browser recovery storage is unavailable. Save your draft before closing this tab."); }
}
function updateStatus(message?: string) {
  saveButton.disabled = !!current?.document?.readonly || saving || !dirty();
  $("save-status").textContent = message ?? (current?.document?.readonly ? "Reference only" : saving ? "Saving…" : dirty() ? "Unsaved changes" : current?.changed ? "Saved locally · edited" : "Matches fetched source");
  $("length").textContent = current?.document ? `${new TextEncoder().encode(content()).length.toLocaleString()} UTF-8 bytes` : `${content().length.toLocaleString()} UTF-16 units`;
  $("description-count").textContent = `${[...description.value.trim()].length} / 160 characters`;
}
function changed() {
  if (suppress || !current) return;
  remember(); updateStatus(); schedulePreview();
}
function sidebar() {
  const filter = $<HTMLInputElement>("search").value.toLowerCase();
  $("notes").replaceChildren();
  const category = $<HTMLSelectElement>("collection-filter").value;
  for (const note of notes.filter(n => (category === "all" || (n.document?.category ?? "notes") === category) && `${n.name} ${n.short_desc} ${n.document?.title ?? ""} ${n.document?.source ?? ""}`.toLowerCase().includes(filter))) {
    const button = document.createElement("button");
    button.type = "button";
    button.setAttribute("aria-current", String(note.name === current?.name));
    button.title = note.document ? `${note.document.title}\n${note.document.source}` : note.short_desc || "No AI routing description";
    const title = document.createElement("span"); title.className = "note-title"; title.textContent = note.document?.title ?? note.name; button.append(title);
    if (note.changed) { const tag = document.createElement("span"); tag.className = "edited-tag"; tag.textContent = "edited"; button.append(tag); }
    button.onclick = () => selectNote(note);
    $("notes").append(button);
  }
  $("count").textContent = String(notes.length);
  $("sync-summary").textContent = `${notes.filter(note => note.changed).length} of ${notes.length} documents edited locally`;
}
function selectNote(note: Draft) {
  if (current?.name === note.name) return;
  if (saving) { notice("Please wait for the current save to finish."); return; }
  if (dirty()) {
    if (!confirm("Discard the unsaved changes to this document? Cancel to stay here and save them.")) return;
    try { localStorage.removeItem(recoveryKey()); } catch {}
  }
  notice(); current = note; suppress = true;
  let draft = { content: note.content, short_desc: note.short_desc };
  try {
    const stored = localStorage.getItem(recoveryKey());
    if (stored) {
      const recovered = JSON.parse(stored);
      if (typeof recovered.content === "string" && typeof recovered.short_desc === "string") {
        if (recovered.revision === note.revision || confirm("A browser recovery draft is older than the saved copy. Restore its text for review?")) {
          draft = recovered; notice("Recovered unsaved browser text. Review it, then save the local draft.");
        } else localStorage.removeItem(recoveryKey());
      }
    }
  } catch { notice("Could not read browser recovery storage."); }
  editor?.destroy();
  editor = new EditorView({
    doc: draft.content,
    parent: $("editor"),
    extensions: [basicSetup, oneDark, EditorView.lineWrapping, EditorView.editable.of(!note.document?.readonly), note.document?.language === "go" ? go() : markdown({
      codeLanguages: info => ["json", "components-v2", "random"].includes(info) ? jsonLanguage : ["faq", "md", "markdown"].includes(info) ? markdownLanguage : null,
    }), EditorView.contentAttributes.of({ "aria-label": `Source for ${note.name}`, spellcheck: "false" }), EditorView.updateListener.of(update => { if (update.docChanged) changed(); })],
  });
  description.value = draft.short_desc;
  $("note-name").textContent = note.document?.title ?? note.name;
  $("description-panel").hidden = !!note.document;
  $("document-meta").hidden = !note.document;
  $("document-scope").textContent = note.short_desc;
  $("document-source").textContent = note.document?.source ?? "";
  $("document-deployment").textContent = note.document?.deployment ?? "";
  $("note-kind").textContent = note.document ? "AI text / " + note.document.category : "Editing note";
  $("source-heading").textContent = note.document?.language === "go" ? "Go source · edit strings in place" : "Markdown source";
  $("preview-tabs").hidden = !!note.document;
  $("document-preview-heading").hidden = !note.document;
  $("document-preview-heading").textContent = note.document?.language === "go" ? "Decoded text literals" : "Markdown preview";
  $("message-author").hidden = !!note.document;
  $("refresh").hidden = !!note.document;
  $("syntax-help").hidden = !!note.document;
  $("preview-footnote").textContent = note.document ? (note.document.language === "go" ? "All nonempty string literals in this file, including schemas, labels and non-prompt strings. Runtime concatenation and request data determine what is actually sent. Saving checks Go syntax; deployment also needs a build and tests." : "AI Markdown is shown as ordinary text, not Discord note directives. Local edits are not active on the VPS.") : "Uses the bot’s Markdown directive parser and validation. Discord typography and media may differ.";
  suppress = false;
  sidebar(); updateStatus(); remember(); schedulePreview(0);
}
async function save() {
  if (!current || current.document?.readonly || saving || !dirty()) return;
  const captured = { content: content(), short_desc: description.value, revision: current.revision };
  const name = current.name;
  saving = true; notice(); updateStatus();
  try {
    const saved: Draft = await api(`/api/notes/${encodeURIComponent(name)}`, captured, "PUT");
    notes[notes.findIndex(note => note.name === name)] = saved;
    if (current?.name === name) {
      current = saved;
      if (content() === captured.content && description.value === captured.short_desc) {
        suppress = true;
        if (content() !== saved.content) editor.dispatch({ changes: { from: 0, to: editor.state.doc.length, insert: saved.content } });
        description.value = saved.short_desc;
        suppress = false;
      }
      remember(); schedulePreview(0);
    }
    sidebar();
  } catch (error) { notice(String((error as Error).message)); }
  finally { saving = false; updateStatus(); }
}
function schedulePreview(delay = 350) {
  clearTimeout(previewTimer); previewAbort?.abort();
  const version = ++previewVersion;
  previewTimer = setTimeout(async () => {
    if (!current) return;
    previewAbort = new AbortController();
    try {
      const result = await api("/api/preview", { content: content(), literal: !current.markup_enabled && !dirty(), ...(current.document ? { document_name: current.name } : {}) }, "POST", previewAbort.signal);
      if (version !== previewVersion) return;
      latestPreview = result.rendered;
      latestLiterals = result.literals ?? [];
      $("preview-error").hidden = true;
      renderPreview();
    } catch (error) {
      if (version !== previewVersion || (error as Error).name === "AbortError") return;
      latestPreview = undefined;
      $("preview").replaceChildren();
      $("preview-error").textContent = (error as Error).message;
      $("preview-error").hidden = false;
    }
  }, delay);
}
function appendMarkdown(parent: HTMLElement, source: string) {
  if (!source.trim()) return;
  const block = document.createElement("div"); block.className = "markdown";
  block.innerHTML = DOMPurify.sanitize(marked.parse(source, { async: false, gfm: true, breaks: true }), {
    USE_PROFILES: { html: true }, FORBID_TAGS: ["style", "form", "input", "select", "button"], FORBID_ATTR: ["style", "id"],
  });
  for (const link of block.querySelectorAll("a")) { link.target = "_blank"; link.rel = "noopener noreferrer"; }
  for (const image of block.querySelectorAll("img")) { image.loading = "lazy"; image.referrerPolicy = "no-referrer"; }
  parent.append(block);
}
function image(url: string, alt: string, className = "") {
  const element = document.createElement("img"); element.src = url; element.alt = alt; element.className = className;
  element.loading = "lazy"; element.referrerPolicy = "no-referrer";
  return element;
}
function renderPreview() {
  const preview = $("preview"); preview.replaceChildren();
  const rendered = latestPreview;
  if (!rendered) return;
  if (current?.document?.language === "go") {
    for (const literal of latestLiterals) {
      const details = document.createElement("details"); details.className = "source-literal";
      const summary = document.createElement("summary"); summary.textContent = `Line ${literal.line}: ${literal.content.replace(/\s+/g, " ").slice(0, 100)}`;
      const text = document.createElement("pre"); text.textContent = literal.content;
      details.append(summary, text); preview.append(details);
    }
    return;
  }
  if (current?.document || readable || (!rendered.Layout && !rendered.FAQ)) { appendMarkdown(preview, rendered.Text); return; }
  const container = document.createElement("div"); container.className = "rich-container";
  appendMarkdown(container, rendered.Before);
  if (rendered.Layout) for (const component of rendered.Layout.components) {
    switch (component.Type) {
      case "text": appendMarkdown(container, component.Content); break;
      case "separator": container.append(document.createElement("hr")); break;
      case "section": {
        const section = document.createElement("div"); section.className = "component-section";
        appendMarkdown(section, component.Content); section.append(image(component.Thumbnail, "Section thumbnail", "thumbnail")); container.append(section); break;
      }
      case "gallery": {
        const gallery = document.createElement("div"); gallery.className = "gallery";
        for (const [index, url] of component.URLs.entries()) {
          const link = document.createElement("a"); link.href = url; link.target = "_blank"; link.rel = "noopener noreferrer";
          if (/\.(mp4|webm)(\?|$)/i.test(url)) {
            const video = document.createElement("video"); video.src = url; video.controls = true; video.preload = "none"; video.setAttribute("aria-label", `Gallery video ${index + 1}`); gallery.append(video);
          } else { link.append(image(url, `Gallery media ${index + 1}`)); gallery.append(link); }
        }
        container.append(gallery); break;
      }
      case "buttons": {
        const row = document.createElement("div"); row.className = "component-buttons";
        for (const button of component.Buttons) {
          const link = document.createElement("a"); link.textContent = button.label; link.href = button.url; link.target = "_blank"; link.rel = "noopener noreferrer"; row.append(link);
        }
        container.append(row); break;
      }
    }
  }
  if (rendered.FAQ) {
    const faq = rendered.FAQ;
    appendMarkdown(container, (faq.Header || "# FAQ") + "\nChoose a question below.");
    const answer = document.createElement("div"); answer.className = "faq-answer";
    const select = document.createElement("select"); select.className = "faq-select"; select.setAttribute("aria-label", "Choose a FAQ question");
    select.add(new Option("Choose a question", ""));
    for (const [index, item] of faq.Items.entries()) select.add(new Option(item.Question, String(index)));
    select.onchange = () => { answer.replaceChildren(); if (select.value !== "") { const item = faq.Items[Number(select.value)]; appendMarkdown(answer, `## ${item.Question}\n${item.Answer}`); } };
    container.append(answer, select);
  }
  appendMarkdown(container, rendered.After); preview.append(container);
}
async function load() {
  const data = await api("/api/notes");
  notes = data.notes;
  storagePrefix = `metrobot-notes:${data.fetched_at}:`;
  $("login").hidden = true; $("workspace").hidden = false;
  if (notes.length) selectNote(notes[0]);
}
async function login(token: string) {
  await api("/session", { token });
  history.replaceState(null, "", location.pathname);
  await load();
}
$("save").onclick = save;
description.oninput = changed;
$("search").oninput = sidebar;
$("collection-filter").onchange = sidebar;
$("refresh").onclick = () => schedulePreview(0);
$("discord-tab").onclick = () => { readable = false; $("discord-tab").setAttribute("aria-pressed", "true"); $("readable-tab").setAttribute("aria-pressed", "false"); renderPreview(); };
$("readable-tab").onclick = () => { readable = true; $("discord-tab").setAttribute("aria-pressed", "false"); $("readable-tab").setAttribute("aria-pressed", "true"); renderPreview(); };
window.addEventListener("keydown", event => { if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") { event.preventDefault(); void save(); } });
window.addEventListener("beforeunload", event => { if (dirty() || saving) { event.preventDefault(); event.returnValue = ""; } });
$("login-form").onsubmit = async event => {
  event.preventDefault();
  try { await login($<HTMLInputElement>("access-key").value.trim()); $<HTMLInputElement>("access-key").value = ""; }
  catch (error) { $("login-error").textContent = (error as Error).message; }
};
try {
  if (location.hash.length > 1) await login(location.hash.slice(1));
  else await load();
} catch (error) { $("workspace").hidden = true; $("login").hidden = false; $("login-error").textContent = (error as Error).message; }
