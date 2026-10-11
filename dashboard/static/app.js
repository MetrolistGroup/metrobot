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
let memoryBaseline = null;

function noteValues() {
  return { name: $("note-name").value, short_desc: $("note-description").value, content: $("note-content").value };
}

function noteDirty() {
  return noteBaseline !== null && JSON.stringify(noteValues()) !== JSON.stringify(noteBaseline);
}

function memoryDirty() {
  return memoryBaseline !== null && $("memory-content").value !== memoryBaseline;
}

function hasDraft() {
  return noteDirty() || memoryDirty() || $("channel-id").value !== "" || $("admin-id").value !== "";
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
  navigation.forEach((button) => { button.disabled = busy; });
  $("logout").hidden = !canLogout;
  $("logout").disabled = busy;
  $("retry").disabled = busy;
  $("app").setAttribute("aria-busy", String(busy));
  $("note-name").readOnly = locked || selectedName !== null;
  $("note-description").readOnly = locked;
  $("note-content").readOnly = locked;
  $("channel-id").readOnly = locked;
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
  if (!state || !state.user.permanent) {
    $("admin-id").readOnly = true;
    $("add-admin").disabled = true;
  }
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
}

function setNote(note) {
  selectedName = note ? note.name : null;
  $("note-name").value = note ? note.name : "";
  $("note-description").value = note ? note.short_desc : "";
  $("note-content").value = note ? note.content : "";
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
    button.className = "note-choice";
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

function renderIDs(listID, entries, kind) {
  const list = $(listID);
  list.replaceChildren();
  entries.forEach((entry) => {
    const id = kind === "admins" ? entry.id : entry;
    const permanent = kind === "admins" && entry.permanent;
    const item = document.createElement("li");
    const details = document.createElement("div");
    const text = document.createElement("span");
    text.textContent = id;
    details.append(text);
    if (kind === "admins") {
      const role = document.createElement("span");
      role.className = "label";
      role.textContent = permanent ? "Permanent admin" : "Admin";
      details.append(role);
    }
    item.append(details);
    if (!permanent) {
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "danger";
      remove.textContent = "Remove";
      remove.setAttribute("aria-label", `Remove ${kind === "admins" ? "admin" : "channel"} ${id}`);
      remove.addEventListener("click", () => {
        if (busy || !authorized) return;
        const question = kind === "admins" ? `Remove administrator access for Discord user ${id}?` : `Remove channel ${id} from the exclusion list?`;
        if (window.confirm(question)) mutate(`/api/${kind}/remove`, { id }, "Entry removed.");
      });
      item.append(remove);
    }
    list.append(item);
  });
}

function renderState() {
  $("loading").hidden = true;
  $("auth").hidden = true;
  $("app").hidden = false;
  $("identity").hidden = false;
  $("user-name").textContent = state.user.name;
  $("user-id").textContent = state.user.id;
  $("user-role").textContent = state.user.permanent ? "Permanent admin" : "Admin";
  $("admins-nav").hidden = !state.user.permanent;
  if (section === "admins" && !state.user.permanent) showSection("notes");
  if (noteBaseline === null) setNote(state.notes[0] || null);
  else if (!noteDirty() && selectedName !== null) {
    setNote(state.notes.find((note) => note.name === selectedName) || state.notes[0] || null);
  }
  renderNotes();
  if (!memoryDirty()) {
    $("memory-content").value = state.memory;
    memoryBaseline = state.memory;
  }
  $("memory-unavailable").hidden = state.memory_available;
  validateMemory();
  renderIDs("channels-list", state.excluded_channels, "channels");
  $("channels-empty").hidden = state.excluded_channels.length !== 0;
  renderIDs("admins-list", state.user.permanent ? state.admins : [], "admins");
  $("admins-empty").hidden = state.admins.length !== 0;
  updateControls();
}

async function request(path, payload) {
  let response;
  try {
    response = await fetch(path, {
      method: payload === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: payload === undefined ? {} : { "Content-Type": "application/json", "X-CSRF-Token": state.csrf_token },
      body: payload === undefined ? undefined : JSON.stringify(payload),
    });
  } catch {
    throw new Error("Could not reach the server. Check your connection and retry state before submitting again.");
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
      !Array.isArray(next.notes) || !Array.isArray(next.excluded_channels) || !Array.isArray(next.admins) ||
      typeof next.memory !== "string" || typeof next.memory_available !== "boolean") {
    throw new Error("The server returned an invalid administration state.");
  }
  state = next;
  authorized = true;
  canLogout = true;
  renderState();
  $("retry").hidden = true;
}

function failure(error, prefix = "", lock = false) {
  $("loading").hidden = true;
  if (lock || error.status === 401 || error.status === 403) authorized = false;
  if (error.status === 401 || error.status === 403) {
    $("auth").hidden = false;
    const denied = error.status === 403;
    $("auth-title").textContent = denied ? "Administrator access required" : "Sign in to Metrobot";
    $("auth-description").textContent = state
      ? "Your session or access is no longer valid. Unsaved text is kept below for copying. Signing in again leaves this page; copy your draft first."
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
  $("note-description").setCustomValidity($("note-description").value.trim() ? "" : "A short description is required.");
  $("note-content").setCustomValidity($("note-content").value.trim() ? "" : "Note content is required.");
}

function validateMemory() {
  // The memory store trims input and adds a final newline before enforcing its byte limit.
  const content = $("memory-content").value.trim();
  const size = new TextEncoder().encode(content + "\n").length;
  $("memory-size").textContent = `${size.toLocaleString()} / 16,384 bytes when saved`;
  $("memory-content").setCustomValidity(size > 16384 ? "Shared memory must fit within 16 KiB, including its final newline." : content ? "" : "Memory content is required. Use Clear memory to reset it.");
}

navigation.forEach((button) => {
  button.addEventListener("click", () => {
    if (!busy && (button.dataset.section !== "admins" || state.user.permanent)) showSection(button.dataset.section);
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
[["channel", "channels"], ["admin", "admins"]].forEach(([name, endpoint]) => {
  $(`${name}-form`).addEventListener("submit", (event) => {
    event.preventDefault();
    if (!$(`${name}-form`).reportValidity() || busy || !authorized || (name === "admin" && !state.user.permanent)) return;
    const id = $(`${name}-id`).value;
    mutate(`/api/${endpoint}/add`, { id }, name === "admin" ? "Discord admin added." : "Channel exclusion added.", () => { $(`${name}-id`).value = ""; });
  });
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
    $("channel-id").value = "";
    $("admin-id").value = "";
    $("notes-list").replaceChildren();
    $("channels-list").replaceChildren();
    $("admins-list").replaceChildren();
    $("app").hidden = true;
    $("identity").hidden = true;
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
refresh();
