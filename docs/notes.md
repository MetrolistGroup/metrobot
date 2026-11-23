# Note markup

Notes store Markdown in `bot.db`. Directives inside a note body control behavior; note names do not. Renaming a note keeps its behavior, and removing a directive disables it. Users retrieve notes with `/note <name>`, `.name`, or inline `!!name` on Discord and Telegram.

Admin commands validate markup and `short_desc` before saving. `short_desc` must be nonblank, single-line, and at most 160 characters. Invalid markup never replaces an existing note. Raw database reads return the stored text without shuffling or rendering. Legacy literal `\n` and `\r\n` sequences normalize into newlines outside JSON strings and non-`faq` code fences.

## Grammar

Top-level backtick fences with the exact info string `random`, `faq`, or `components-v2` act as directives.

- Open with at least three backticks and up to three leading spaces.
- Close with at least as many backticks and no trailing non-whitespace text. Unclosed blocks fail validation.
- Tilde fences, indented code blocks, blockquotes, lists, unknown fence info strings, and directives inside longer outer fences stay literal Markdown.
- Multiple `random` blocks are allowed. Only one rich block, either `faq` or `components-v2`, is allowed per note.
- JSON blocks must hold one nonempty JSON value with valid UTF-8, paired Unicode surrogates, and no unknown fields.

## Random groups

Each retrieval shuffles all array entries, joins them with newlines, and keeps surrounding Markdown fixed. Entries must be nonempty Markdown strings and are not parsed recursively for directives.

````markdown
Official links:
```random
[
  "Source: https://github.com/MetrolistGroup/Metrolist",
  "Website: https://metrolist.cc"
]
```
````

## FAQ

An `faq` block allows an optional `#` title and intro followed by 1 to 25 top-level `##` questions with nonempty answers. Headings inside code, quotes, or lists, other heading levels, and setext headings stay ordinary answer text. When no header precedes the first `##` question, the default header is `# FAQ` above `Choose a question below`.

````markdown
```faq
# Release FAQ

## Where are official releases?
https://github.com/MetrolistGroup/Metrolist/releases

## What should a bug report include?
Your app and build version, platform, exact error, and steps to reproduce.
```
````

Selector labels and option previews are trimmed to 100 UTF-16 units on Discord; full answers are untouched. Each FAQ page, including surrounding Markdown, must fit 4,000 UTF-16 units.

## Components V2

A `components-v2` JSON object must contain only `components`, an array of 1 to 10 supported items wrapped in a Discord container. Custom IDs, colors, scripts, and arbitrary payloads are rejected.

| Type | Required fields |
| --- | --- |
| `text` | Nonempty Markdown `content` |
| `separator` | None |
| `section` | Nonempty `content` and HTTPS `thumbnail` |
| `gallery` | `urls` array of 1 to 10 HTTPS URLs |
| `buttons` | `buttons` array of 1 to 5 objects with `label` and HTTPS `url` |

Button labels require 1 to 80 UTF-16 units. URLs must be absolute HTTPS without credentials or whitespace and at most 512 UTF-16 units. A message may have at most 10 direct container children, 40 total components including nested children, and 4,000 UTF-16 text units including surrounding text and any donor-welcome prefix.

````markdown
```components-v2
{
  "components": [
    {"type": "text", "content": "# Metrolist"},
    {"type": "separator"},
    {"type": "section", "content": "Official links", "thumbnail": "https://github.com/MetrolistGroup.png"},
    {"type": "buttons", "buttons": [
      {"label": "Releases", "url": "https://github.com/MetrolistGroup/Metrolist/releases"},
      {"label": "Source", "url": "https://github.com/MetrolistGroup/Metrolist"}
    ]}
  ]
}
```
````

## Delivery

- **Discord.** Note commands, triggers, donor welcomes, and support deliver the authored layout. Rich notes suppress all mentions, while donor welcomes allow only the new donor's ping. Plain `/note` commands allow user mentions without role or everyone pings. Plain notes over 2,000 UTF-16 units, or rejected rich layouts, fall back to readable text or a `note.md` file while keeping visibility and mention rules. Legacy `note:kmp:question` menus stay routed to the `kmp` FAQ while it has an `faq` block.
- **Telegram.** Notes render headings, bold text, inline code, bullet lists, and `[label](url)` Markdown links as Telegram HTML. FAQs send inline question buttons that edit the message to the chosen answer, or reply with `note.md` if an edited page exceeds 4,096 UTF-16 units. Layouts with `buttons` attach URL button rows, and `separator` becomes `---`. Plain notes or layouts exceeding 4,096 UTF-16 units or 4,096 serialized HTML units are sent as `note.md`.
- **AI callers.** Support and chat tools read rendered readable Markdown with shuffled random entries, all FAQ sections, and labeled layout URLs.

## Legacy migration

On first open, `note_markup_v1` runs once inside a transaction, adds `markup_enabled`, and clears it on existing rows. If present and valid UTF-8 without top-level directives, `donate` keeps its intro line and wraps remaining lines in a `random` block, and `kmp` wraps its body in a `faq` fence. Ambiguous notes stay literal, and invalid UTF-8 bytes stay untouched until an admin edits them. Editing a legacy note through a validated command enables markup. The migration records its marker even on an empty database so restarts never repeat it.

## Metrolist v14 note migration

After `note_markup_v1`, `metrolist_v14_notes_v1` runs once in a transaction and compares stored notes against `db/note_versions/pre_v14*.json`, also matching `note_markup_v1`-wrapped `kmp`. Unchanged matches for the 15 notes in `docs/support-notes.json` receive the current body, `short_desc`, and active `markup_enabled` flag from `db/note_versions/v14.json`.

Absent notes, already-current notes, and staff-edited notes are skipped and logged for manual review. The migration does not alter `garmin-memory.md`, `garmin_base_system_prompt`, or `testertour`. Donor welcomes now use `donorwelcome` when present or a public thank-you fallback, never `testertour`.
