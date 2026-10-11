"use strict";

const archives = {
  cases: { sequence: 0, controller: null, cursor: "", loaded: false, loading: false, count: 0, ids: new Set() },
  logs: { sequence: 0, controller: null, cursor: "", loaded: false, loading: false, count: 0, ids: new Set(), scanned: 0, partial: false, warnings: new Set() },
};

function updateArchiveControls() {
  for (const [kind, view] of Object.entries(archives)) {
    const locked = busy || !authorized || view.loading;
    $(`${kind}-form`).querySelector("button").disabled = locked;
    $(`${kind}-more`).disabled = locked;
    $(`${kind}-more`).hidden = !view.cursor;
    $(`${kind}-loading`).hidden = !view.loading;
    $(`${kind}-section`).setAttribute("aria-busy", String(view.loading));
  }
}

function clearArchive(kind, message) {
  const view = archives[kind];
  view.sequence++;
  view.controller?.abort();
  Object.assign(view, { controller: null, cursor: "", loaded: false, loading: false, count: 0, ids: new Set() });
  $(`${kind}-list`).replaceChildren();
  $(`${kind}-status`).textContent = message;
  if (kind === "logs") {
    view.scanned = 0;
    view.partial = false;
    view.warnings.clear();
    $("logs-warnings").replaceChildren();
  }
  updateArchiveControls();
}

function clearArchives() {
  clearArchive("cases", "Open this section to load cases.");
  clearArchive("logs", "Choose dates and search.");
}

function archiveFilters(kind) {
  const from = $(`${kind}-from`).value;
  const to = $(`${kind}-to`).value;
  $(`${kind}-to`).setCustomValidity(from && to && from > to ? "The end day must be on or after the start day." : "");
  if (!$(`${kind}-form`).reportValidity()) return null;
  return { from, to, q: $(`${kind}-q`).value.trim(), ...(kind === "logs" ? { source: $("logs-source").value } : {}) };
}

function utcTimestamp(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value) : date.toISOString().replace("T", " ").replace(/\.\d{3}Z$/, " UTC");
}

function appendCase(item) {
  const row = document.createElement("tr");
  const values = [String(item.case_number), utcTimestamp(item.timestamp), `${item.platform}\n${item.action_type}`,
    `Target: ${item.target_id}\nModerator: ${item.moderator_id}`, item.reason];
  values.forEach((value) => {
    const cell = document.createElement("td");
    cell.textContent = value;
    row.append(cell);
  });
  $("cases-list").append(row);
}

function safeLink(url, label) {
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== "https:" || parsed.username || parsed.password) return null;
    const link = document.createElement("a");
    link.href = parsed.href;
    link.textContent = label;
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    return link;
  } catch { return null; }
}

function appendLog(item) {
  const entry = document.createElement("li");
  const heading = document.createElement("div");
  heading.className = "log-heading";
  const time = document.createElement("span");
  time.textContent = `${utcTimestamp(item.timestamp)} / ${item.source}`;
  const id = document.createElement("span");
  id.className = "mono";
  id.textContent = item.id;
  heading.append(time, id);
  const content = document.createElement("pre");
  content.className = "plain-text";
  content.textContent = item.content;
  const links = document.createElement("div");
  links.className = "log-links";
  const jump = safeLink(item.url, "Open in Discord");
  if (jump) links.append(jump);
  item.attachments.forEach((attachment) => {
    const link = safeLink(attachment.url, attachment.name || "Attachment");
    if (link) links.append(link);
  });
  entry.append(heading, content, links);
  $("logs-list").append(entry);
}

async function loadArchive(kind, more = false) {
  const view = archives[kind];
  if (busy || !authorized || view.loading || (more && !view.cursor)) return;
  const query = archiveFilters(kind);
  if (!query) return;
  if (!more) clearArchive(kind, "");
  const sequence = ++view.sequence;
  const controller = new AbortController();
  view.controller = controller;
  view.loading = true;
  $(`${kind}-status`).textContent = kind === "logs" ? "Scanning Discord history…" : "Loading cases…";
  updateArchiveControls();
  try {
    const page = await request(`/api/${kind}`, undefined, { query: { ...query, before: more ? view.cursor : "" }, signal: controller.signal });
    if (sequence !== view.sequence || !authorized) return;
    if (!Array.isArray(page.items) || typeof page.next_cursor !== "string" || (kind === "logs" &&
        (typeof page.scanned !== "number" || typeof page.complete !== "boolean" || !Array.isArray(page.warnings) ||
        page.warnings.some((warning) => typeof warning !== "string") || page.items.some((item) => !Array.isArray(item.attachments))))) {
      throw new Error("The server returned an invalid archive page.");
    }
    page.items.forEach((item) => {
      const id = kind === "cases" ? item.case_number : item.id;
      if (view.ids.has(id)) return;
      view.ids.add(id);
      (kind === "cases" ? appendCase : appendLog)(item);
      view.count++;
    });
    view.cursor = page.next_cursor;
    view.loaded = true;
    if (kind === "cases") {
      $("cases-status").textContent = view.count
        ? `${view.count.toLocaleString()} cases shown.${view.cursor ? " More cases available." : " End of results."}`
        : "No cases match these filters.";
    } else {
      view.scanned += page.scanned;
      page.warnings.forEach((warning) => view.warnings.add(warning));
      $("logs-warnings").replaceChildren();
      view.warnings.forEach((warning) => {
        const item = document.createElement("li");
        item.textContent = warning;
        $("logs-warnings").append(item);
      });
      const progress = `${view.scanned.toLocaleString()} messages scanned.`;
      view.partial ||= page.warnings.some((warning) => warning.startsWith("Partial search:")) || (!page.complete && !view.cursor);
      const complete = page.complete && !view.cursor && !view.partial;
      $("logs-status").textContent = view.count
        ? `${view.count.toLocaleString()} matching logs. ${progress} ${complete ? "Search complete." : view.cursor ? "More history remains; continue search." : "Search incomplete; see warnings or retry."}`
        : `${complete ? "No logs match these dates and text." : "No matches yet in the portion scanned."} ${progress} ${!complete ? (view.cursor ? "More history remains; continue search." : "Search incomplete; see warnings or retry.") : "Search complete."}`;
      $("logs-more").textContent = page.items.length === 0 ? "Continue search" : "Load more";
    }
  } catch (error) {
    if (sequence !== view.sequence || error.name === "AbortError") return;
    $(`${kind}-status`).textContent = `Search failed: ${error.message}${view.count ? " Previously loaded results remain." : ""}`;
    failure(error);
  } finally {
    if (sequence === view.sequence) {
      view.loading = false;
      view.controller = null;
      updateArchiveControls();
    }
  }
}

function initArchives() {
  const today = new Date().toISOString().slice(0, 10);
  $("logs-from").value = today;
  $("logs-to").value = today;
  for (const kind of Object.keys(archives)) {
    $(`${kind}-form`).addEventListener("submit", (event) => { event.preventDefault(); loadArchive(kind); });
    $(`${kind}-more`).addEventListener("click", () => loadArchive(kind, true));
    const invalidate = () => {
      $(`${kind}-to`).setCustomValidity("");
      clearArchive(kind, "Filters changed. Search to load matching results.");
    };
    $(`${kind}-form`).addEventListener("input", invalidate);
    $(`${kind}-form`).addEventListener("change", invalidate);
  }
}
