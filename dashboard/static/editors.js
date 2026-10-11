"use strict";

if (window.Prism) {
  Prism.languages.random = Prism.languages.json;
  Prism.languages["components-v2"] = Prism.languages.json;
  Prism.languages.faq = Prism.languages.markdown;
}

const highlightedEditors = [...document.querySelectorAll("[data-highlight]")].map((input) => {
  const box = input.parentElement;
  const pre = box.querySelector("pre");
  let previous = null;
  let releaseTab = false;
  const syncScroll = () => { pre.scrollTop = input.scrollTop; pre.scrollLeft = input.scrollLeft; };
  const sync = () => {
    if (previous !== input.value) {
      previous = input.value;
      if (window.Prism) {
        // Prism encodes source before emitting its own highlight spans, never API HTML.
        pre.innerHTML = Prism.highlight(previous + "\n", Prism.languages.markdown, "markdown");
        box.classList.add("highlighted");
      }
    }
    syncScroll();
  };
  input.addEventListener("input", sync);
  input.addEventListener("scroll", syncScroll);
  input.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { releaseTab = true; return; }
    if (event.key === "Tab" && !event.shiftKey && !releaseTab && !input.readOnly && !input.disabled) {
      event.preventDefault();
      input.setRangeText("  ", input.selectionStart, input.selectionEnd, "end");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    }
    releaseTab = false;
  });
  input.addEventListener("blur", () => { releaseTab = false; });
  if (window.ResizeObserver) new ResizeObserver(syncScroll).observe(box);
  sync();
  return sync;
});

function refreshEditors() { highlightedEditors.forEach((sync) => sync()); }

const noteExamples = {
  random: '```random\n[\n  "**First tip:** Read the pinned guide.",\n  "**Second tip:** Include your device model."\n]\n```',
  faq: '```faq\n# Getting started\n\n## Where is the guide?\nRead the pinned messages in the support channel.\n\n## What should I include?\nYour device model and the steps you already tried.\n```',
  layout: '```components-v2\n{\n  "components": [\n    {"type": "text", "content": "## Support resources\\nRead the guide before asking."},\n    {"type": "separator"},\n    {"type": "buttons", "buttons": [{"label": "Discord help", "url": "https://support.discord.com/"}]}\n  ]\n}\n```',
};

document.querySelectorAll("[data-example]").forEach((button) => {
  button.addEventListener("click", () => {
    const input = document.getElementById("note-content");
    if (input.readOnly || input.disabled) return;
    const cursor = input.selectionStart;
    const before = input.value.slice(0, cursor);
    const after = input.value.slice(cursor);
    const prefix = before && !before.endsWith("\n\n") ? (before.endsWith("\n") ? "\n" : "\n\n") : "";
    const suffix = after && !after.startsWith("\n\n") ? (after.startsWith("\n") ? "\n" : "\n\n") : "\n";
    input.setRangeText(prefix + noteExamples[button.dataset.example] + suffix, cursor, cursor, "end");
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.focus();
  });
});
