"use strict";

const $ = (id) => document.getElementById(id);
const navigation = [...document.querySelectorAll("[data-section]")];
let state = null;
let authorized = false;
let canLogout = false;
let busy = false;
let section = "notes";
let selectedName = null;
let noteBaseline = null;
let rawNoteContent = "";
let memoryBaseline = null;
let promptBaseline = null;
let promptData = null;
let promptLoading = false;
let promptController = null;
let promptSequence = 0;

function noteValues() {
  const content = $("note-content").value;
  // Textareas normalize line endings; keep the raw note when only metadata changes.
  return { name: $("note-name").value, short_desc: $("note-description").value,
    content: content === rawNoteContent.replace(/\r\n?/g, "\n") ? rawNoteContent : content };
}

function noteDirty() {
  return noteBaseline !== null && JSON.stringify(noteValues()) !== JSON.stringify(noteBaseline);
}

function memoryDirty() {
  return memoryBaseline !== null && $("memory-content").value !== memoryBaseline;
}

function promptDirty() {
  return promptBaseline !== null && $("prompt-content").value !== promptBaseline;
}

function hasDraft() {
  return noteDirty() || memoryDirty() || promptDirty() || $("admin-id").value !== "";
}

function status(message, kind = "info") {
  const element = $("status");
  element.dataset.kind = kind;
  element.setAttribute("role", kind === "error" ? "alert" : "status");
  element.setAttribute("aria-live", kind === "error" ? "assertive" : "polite");
  element.textContent = message;
  if (kind === "error") element.focus();
}

function updateControls() {
  const locked = busy || !authorized;
  const noteChanged = noteDirty();
  const memoryChanged = memoryDirty();
  document.querySelectorAll("#app button").forEach((button) => { button.disabled = locked; });
  $("logout").hidden = !canLogout;
  $("logout").disabled = busy;
  $("retry").disabled = busy;
  $("app").setAttribute("aria-busy", String(busy));
  $("note-name").readOnly = locked || selectedName !== null;
  $("note-description").readOnly = locked;
  $("note-content").readOnly = locked;
  $("admin-id").readOnly = locked;
  $("rename-note").disabled = locked || selectedName === null;
  $("delete-note").disabled = locked || selectedName === null;
  $("save-note").disabled = locked || (selectedName !== null && !noteChanged);
  const noteStatus = noteChanged ? "Unsaved changes" : "";
  if ($("note-draft").textContent !== noteStatus) $("note-draft").textContent = noteStatus;
  const unavailable = !state || !state.memory_available;
  $("memory-content").disabled = unavailable;
  $("memory-content").readOnly = locked;
  $("replace-memory").disabled = locked || unavailable || !memoryChanged;
  $("clear-memory").disabled = locked || unavailable;
  const memoryStatus = memoryChanged ? "Unsaved changes" : "";
  if ($("memory-draft").textContent !== memoryStatus) $("memory-draft").textContent = memoryStatus;
  const permanent = state?.user.permanent === true;
  if (!permanent) {
    $("admin-id").readOnly = true;
    $("add-admin").disabled = true;
  }
  $("prompt-content").readOnly = locked || !permanent || promptLoading || !promptData;
  $("save-prompt").disabled = locked || !permanent || promptLoading || !promptData || !promptDirty();
  $("reset-prompt").disabled = locked || !permanent || promptLoading || !promptData || (!promptData.overridden && !promptDirty());
  $("prompt-retry").disabled = locked || !permanent || promptLoading;
  $("prompt-draft").textContent = promptDirty() ? "Unsaved changes" : "";
  $("prompt-loading").hidden = !promptLoading;
  $("prompt-section").setAttribute("aria-busy", String(promptLoading));
  updateArchiveControls();
  refreshEditors();
}

function setBusy(value) {
  busy = value;
  updateControls();
}

function showSection(name) {
  section = name;
  navigation.forEach((button) => {
    const active = button.dataset.section === name;
    if (active) button.setAttribute("aria-current", "page");
    else button.removeAttribute("aria-current");
    $(`${button.dataset.section}-section`).hidden = !active;
  });
  refreshEditors();
  if (authorized && name === "cases" && !archives.cases.loaded) loadArchive("cases");
  if (authorized && name === "prompt" && !promptData) loadPrompt();
}

function setNote(note) {
  selectedName = note ? note.name : null;
  $("note-name").value = note ? note.name : "";
  $("note-description").value = note ? note.short_desc : "";
  rawNoteContent = note ? note.content : "";
  $("note-content").value = rawNoteContent;
  noteBaseline = noteValues();
  $("editor-title").textContent = note ? "Edit note" : "New note";
  $("save-note").textContent = note ? "Save changes" : "Create note";
  validateNote();
}

function chooseNote(note) {
  if (busy || !authorized) return;
  if (note && note.name === selectedName) return;
  if (noteDirty() && !window.confirm("Discard the unsaved note draft and open another note?")) return;
  setNote(note);
  renderNotes();
  updateControls();
  $(note ? "note-content" : "note-name").focus();
}

function renderNotes() {
  const list = $("notes-list");
  list.replaceChildren();
  $("notes-count").textContent = `${state.notes.length} notes`;
  $("notes-empty").hidden = state.notes.length !== 0;
  state.notes.forEach((note) => {
    const item = document.createElement("li");
    const button = document.createElement("button");
    button.type = "button";
    button.className = "note-choice no-round";
    button.setAttribute("aria-controls", "note-form");
    if (note.name === selectedName) button.setAttribute("aria-current", "true");
    const name = document.createElement("strong");
    name.textContent = note.name;
    const description = document.createElement("span");
    description.textContent = note.short_desc;
    button.append(name, description);
    button.addEventListener("click", () => chooseNote(note));
    item.append(button);
    list.append(item);
  });
}

function renderAdmins(entries) {
  const list = $("admins-list");
  list.replaceChildren();
  entries.forEach(({ id, permanent }) => {
    const item = document.createElement("li");
    const details = document.createElement("div");
    const text = document.createElement("span");
    text.textContent = id;
    details.append(text);
    const role = document.createElement("span");
    role.className = "eyebrow";
    role.textContent = permanent ? "Permanent admin" : "Admin";
    details.append(role);
    item.append(details);
    if (!permanent) {
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "border no-round danger";
      remove.textContent = "Remove";
      remove.setAttribute("aria-label", `Remove admin ${id}`);
      remove.addEventListener("click", () => {
        if (busy || !authorized) return;
        if (window.confirm(`Remove administrator access for Discord user ${id}?`)) {
          mutate("/api/admins/remove", { id }, "Discord admin removed.");
        }
      });
      item.append(remove);
    }
    list.append(item);
  });
}

function renderState() {
  $("loading").hidden = true;
  $("auth").hidden = true;
  $("recovery").hidden = true;
  $("recovery-content").value = "";
  $("app").hidden = false;
  $("identity").hidden = false;
  $("user-name").textContent = state.user.name;
  $("user-id").textContent = state.user.id;
  $("user-role").textContent = state.user.permanent ? "Permanent admin" : "Admin";
  $("admins-nav").hidden = !state.user.permanent;
  $("prompt-nav").hidden = !state.user.permanent;
  if (!state.user.permanent) {
    clearPrompt();
    if (section === "admins" || section === "prompt") showSection("notes");
  }
  if (noteBaseline === null) setNote(state.notes[0] || null);
  else if (!noteDirty() && selectedName !== null) {
    setNote(state.notes.find((note) => note.name === selectedName) || state.notes[0] || null);
  }
  renderNotes();
  if (!memoryDirty()) {
    $("memory-content").value = state.memory;
    memoryBaseline = $("memory-content").value;
  }
  $("memory-unavailable").hidden = state.memory_available;
  validateMemory();
  renderAdmins(state.user.permanent ? state.admins : []);
  $("admins-empty").hidden = state.admins.length !== 0;
  updateControls();
}

async function request(path, payload, options = {}) {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(options.query || {})) {
    if (value !== "" && value !== undefined) query.set(key, value);
  }
  const url = query.size ? `${path}?${query}` : path;
  let response;
  try {
    response = await fetch(url, {
      signal: options.signal,
      method: payload === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: payload === undefined ? {} : { "Content-Type": "application/json", "X-CSRF-Token": state.csrf_token },
      body: payload === undefined ? undefined : JSON.stringify(payload),
    });
  } catch (error) {
    if (error.name === "AbortError") throw error;
    throw new Error("Could not reach the server. Check your connection and retry.");
  }
  let data;
  try { data = await response.json(); }
  catch { throw Object.assign(new Error("The server returned an unreadable response."), { status: response.status }); }
  if (!response.ok) {
    throw Object.assign(new Error(typeof data?.error === "string" ? data.error : `Request failed (${response.status}).`), { status: response.status });
  }
  return data;
}

async function readState() {
  const next = await request("/api/state");
  if (!next?.user || typeof next.user.id !== "string" || typeof next.user.name !== "string" ||
      typeof next.user.permanent !== "boolean" || typeof next.csrf_token !== "string" || !next.csrf_token ||
      !Array.isArray(next.notes) || !Array.isArray(next.admins) ||
      typeof next.memory !== "string" || typeof next.memory_available !== "boolean") {
    throw new Error("The server returned an invalid administration state.");
  }
  state = next;
  authorized = true;
  canLogout = true;
  renderState();
  $("retry").hidden = true;
}

function showRecovery() {
  const drafts = [];
  if (noteDirty()) {
    const note = noteValues();
    drafts.push(`Note name: ${note.name}\nShort description: ${note.short_desc}\n\n${note.content}`);
  }
  if (memoryDirty()) drafts.push(`Shared Garmin memory draft:\n\n${$("memory-content").value}`);
  if (promptDirty()) drafts.push(`Garmin system prompt draft:\n\n${$("prompt-content").value}`);
  if ($("admin-id").value) drafts.push(`Admin ID draft: ${$("admin-id").value}`);
  $("recovery-content").value = drafts.join("\n\n-----\n\n");
  $("recovery").hidden = drafts.length === 0;
}

function failure(error, prefix = "", lock = false) {
  $("loading").hidden = true;
  if (lock || error.status === 401 || error.status === 403) authorized = false;
  if (error.status === 401 || error.status === 403) {
    $("app").hidden = true;
    $("identity").hidden = true;
    clearArchives();
    clearPrompt();
    showRecovery();
    $("auth").hidden = false;
    const denied = error.status === 403;
    $("auth-title").textContent = denied ? "Administrator access required" : "Sign in to Metrobot";
    $("auth-description").textContent = state
      ? "Your session or access is no longer valid. Fetched administration data is hidden. Any unsaved drafts are available below for copying before signing in again."
      : "Use a Discord account with Metrobot administrator access.";
    if (!denied) canLogout = false;
    status(prefix + (denied ? "Access denied. " : state ? "Your session has expired. " : "") + error.message,
      !state && error.status === 401 ? "info" : "error");
  } else {
    status(prefix + error.message + (hasDraft() ? " Unsaved text remains on this page." : ""), "error");
  }
  $("retry").hidden = false;
  updateControls();
}

async function refresh() {
  if (busy) return;
  setBusy(true);
  status("Loading administration…");
  try {
    await readState();
    status("Administration loaded.");
    if (section === "prompt" && !promptData) loadPrompt();
  } catch (error) { failure(error, "", true); }
  finally { setBusy(false); }
}

async function mutate(path, payload, message, onSaved = () => {}) {
  if (busy || (!authorized && path !== "/auth/logout") || !state) return;
  setBusy(true);
  status("Saving…");
  let saved = false;
  try {
    const result = await request(path, payload);
    if (result.ok !== true) throw new Error("The server did not confirm the change.");
    saved = true;
    onSaved();
    try { await readState(); }
    catch (error) {
      if (path === "/auth/logout" && error.status === 401) {
        failure(error);
        $("retry").hidden = true;
        status(message, "success");
        return;
      }
      throw error;
    }
    status(message, "success");
  } catch (error) {
    failure(error, saved ? `${message} Could not refresh state: ` : "Change not confirmed: ", saved);
  } finally { setBusy(false); }
}

function validateNote() {
  $("note-name").setCustomValidity(/\s/.test($("note-name").value.trim()) ? "Use a note name without spaces." : "");
  const description = $("note-description").value.trim();
  $("note-description").setCustomValidity(!description ? "A short description is required."
    : /[\r\n]/.test(description) ? "A short description must be one line."
    : [...description].length > 160 ? "A short description cannot exceed 160 Unicode characters." : "");
  $("note-content").setCustomValidity($("note-content").value.trim() ? "" : "Note content is required.");
  const advisory = $("note-delivery");
  const rich = /^```(?:faq|components-v2)[ \t]*$/m.test($("note-content").value);
  advisory.hidden = !rich && noteValues().content.length <= 2000;
  advisory.textContent = rich
    ? "Delivery follows the authored FAQ or components-v2 block, regardless of note name. Use at most one rich block per note; random groups belong outside it."
    : "Long notes are delivered as a full note.md file when they exceed message limits. Saving keeps the full content.";
}

function validateMemory() {
  // The memory store trims input and adds a final newline before enforcing its byte limit.
  const content = $("memory-content").value.trim();
  const size = new TextEncoder().encode(content + "\n").length;
  $("memory-size").textContent = `${size.toLocaleString()} / 16,384 bytes when saved`;
  $("memory-content").setCustomValidity(size > 16384 ? "Shared memory must fit within 16 KiB, including its final newline." : content ? "" : "Memory content is required. Use Clear memory to reset it.");
}

function validatePrompt() {
  const content = $("prompt-content").value;
  const size = new TextEncoder().encode(content).length;
  $("prompt-size").textContent = `${size.toLocaleString()} / 65,536 UTF-8 bytes`;
  $("prompt-content").setCustomValidity(!content.trim() ? "The system prompt cannot be blank."
    : size > 65536 ? "The system prompt must fit within 64 KiB." : "");
}

function clearPrompt(discard = false) {
  promptSequence++;
  promptController?.abort();
  promptController = null;
  promptLoading = false;
  promptData = null;
  $("prompt-default").textContent = "";
  $("prompt-form").hidden = true;
  $("prompt-loading").hidden = true;
  $("prompt-retry").hidden = true;
  $("prompt-status").textContent = "Open this section to load the system prompt.";
  if (discard || !promptDirty()) {
    $("prompt-content").value = "";
    promptBaseline = null;
  }
}

async function loadPrompt() {
  if (!authorized || !state.user.permanent || promptLoading) return false;
  const sequence = ++promptSequence;
  const controller = new AbortController();
  promptController = controller;
  promptLoading = true;
  $("prompt-status").textContent = "Loading system prompt…";
  $("prompt-retry").hidden = true;
  updateControls();
  try {
    const next = await request("/api/prompt", undefined, { signal: controller.signal });
    if (sequence !== promptSequence || !authorized || !state.user.permanent) return false;
    if (typeof next.content !== "string" || typeof next.default_content !== "string" || typeof next.overridden !== "boolean") {
      throw new Error("The server returned an invalid system prompt.");
    }
    if (!promptDirty()) {
      $("prompt-content").value = next.content;
      promptBaseline = $("prompt-content").value;
    }
    promptData = next;
    $("prompt-default").textContent = next.default_content;
    $("prompt-status").textContent = next.overridden ? "Custom base prompt is active." : "Bundled default base prompt is active.";
    $("prompt-form").hidden = false;
    validatePrompt();
    return true;
  } catch (error) {
    if (sequence !== promptSequence || error.name === "AbortError") return false;
    $("prompt-status").textContent = `Could not load the system prompt: ${error.message}`;
    $("prompt-retry").hidden = false;
    failure(error);
    return false;
  } finally {
    if (sequence === promptSequence) {
      promptLoading = false;
      promptController = null;
      updateControls();
    }
  }
}

async function changePrompt(reset = false) {
  if (busy || !authorized || !state.user.permanent || !promptData || promptLoading) return;
  validatePrompt();
  if (!reset && !$("prompt-form").reportValidity()) return;
  if (!window.confirm(reset
      ? "Reset Garmin's live base prompt to the bundled default? Any unsaved prompt draft will be discarded."
      : "Save this base system prompt? It changes live Garmin's subsequent replies for everyone.")) return;
  const content = reset ? promptData.default_content : $("prompt-content").value;
  let saved = false;
  setBusy(true);
  status("Saving system prompt…");
  try {
    const result = await request(`/api/prompt/${reset ? "reset" : "replace"}`, reset ? {} : { content });
    if (result.ok !== true) throw new Error("The server did not confirm the prompt change.");
    saved = true;
    $("prompt-content").value = content;
    promptBaseline = $("prompt-content").value;
    promptData.overridden = !reset;
    if (await loadPrompt()) status(reset ? "System prompt reset to default." : "System prompt saved.", "success");
    else if (authorized) status("System prompt saved, but could not reload it. Retry prompt to confirm the saved text.", "error");
  } catch (error) {
    failure(error, saved ? "Prompt saved. " : "Prompt change not confirmed: ");
  } finally { setBusy(false); }
}

$("prompt-retry").addEventListener("click", loadPrompt);
$("prompt-content").addEventListener("input", () => { validatePrompt(); updateControls(); });
$("prompt-form").addEventListener("submit", (event) => { event.preventDefault(); changePrompt(); });
$("reset-prompt").addEventListener("click", () => changePrompt(true));

navigation.forEach((button) => {
  button.addEventListener("click", () => {
    const permanentOnly = ["admins", "prompt"].includes(button.dataset.section);
    if (!busy && authorized && (!permanentOnly || state.user.permanent)) showSection(button.dataset.section);
  });
});
$("new-note").addEventListener("click", () => chooseNote(null));
$("retry").addEventListener("click", refresh);
$("note-form").addEventListener("input", () => { validateNote(); updateControls(); });
$("memory-content").addEventListener("input", () => { validateMemory(); updateControls(); });
$("note-form").addEventListener("submit", (event) => {
  event.preventDefault();
  validateNote();
  if (!$("note-form").reportValidity()) return;
  const values = noteValues();
  values.name = values.name.trim().toLowerCase();
  mutate(`/api/notes/${selectedName === null ? "create" : "edit"}`, values, "Note saved.", () => {
    selectedName = values.name;
    $("note-name").value = values.name;
    noteBaseline = { ...values };
    rawNoteContent = values.content;
    $("editor-title").textContent = "Edit note";
    $("save-note").textContent = "Save changes";
  });
});
$("rename-note").addEventListener("click", () => {
  if (busy || !authorized || selectedName === null) return;
  const answer = window.prompt("New note name (no spaces). Unsaved content will be kept.", selectedName);
  if (answer === null) return;
  const newName = answer.trim().toLowerCase();
  if (!newName || /\s/.test(newName)) { status("Use a nonempty note name without spaces.", "error"); return; }
  if (newName === selectedName) return;
  mutate("/api/notes/rename", { name: selectedName, new_name: newName }, "Note renamed.", () => {
    selectedName = newName;
    $("note-name").value = newName;
    noteBaseline.name = newName;
    validateNote();
  });
});
$("delete-note").addEventListener("click", () => {
  if (busy || !authorized || selectedName === null) return;
  const warning = noteDirty() ? " Its unsaved draft will also be discarded." : "";
  if (window.confirm(`Permanently delete note "${selectedName}"?${warning}`)) {
    mutate("/api/notes/delete", { name: selectedName }, "Note deleted.", () => { setNote(null); noteBaseline = null; });
  }
});
$("memory-form").addEventListener("submit", (event) => {
  event.preventDefault();
  validateMemory();
  if (!$("memory-form").reportValidity() || busy || !authorized || !state.memory_available) return;
  if (!window.confirm("Replace the shared Garmin memory for everyone with this draft?")) return;
  const content = $("memory-content").value;
  mutate("/api/memory/replace", { content }, "Shared memory replaced.", () => { memoryBaseline = content; });
});
$("clear-memory").addEventListener("click", () => {
  if (busy || !authorized || !state.memory_available) return;
  if (window.confirm("Clear the shared Garmin memory for everyone? Any unsaved memory draft will also be discarded.")) {
    mutate("/api/memory/clear", {}, "Shared memory cleared.", () => {
      $("memory-content").value = "";
      memoryBaseline = "";
    });
  }
});
$("admin-form").addEventListener("submit", (event) => {
  event.preventDefault();
  if (!$("admin-form").reportValidity() || busy || !authorized || !state.user.permanent) return;
  const id = $("admin-id").value;
  mutate("/api/admins/add", { id }, "Discord admin added.", () => { $("admin-id").value = ""; });
});
$("logout").addEventListener("click", () => {
  if (busy || !canLogout) return;
  if (hasDraft() && !window.confirm("Sign out and discard all unsaved drafts?")) return;
  mutate("/auth/logout", {}, "Signed out.", () => {
    state = null;
    authorized = false;
    canLogout = false;
    setNote(null);
    noteBaseline = null;
    memoryBaseline = null;
    $("memory-content").value = "";
    clearArchives();
    clearPrompt(true);
    $("admin-id").value = "";
    $("notes-list").replaceChildren();
    $("admins-list").replaceChildren();
    $("app").hidden = true;
    $("identity").hidden = true;
    $("recovery").hidden = true;
    $("recovery-content").value = "";
    showSection("notes");
  });
});
$("login").addEventListener("click", (event) => {
  if (busy) event.preventDefault();
});
window.addEventListener("beforeunload", (event) => {
  if (hasDraft() || busy) {
    event.preventDefault();
    event.returnValue = "";
  }
});
initArchives();
refresh();
