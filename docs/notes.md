# Note markup

Notes are Markdown. Directives in the body control behavior; names do not. Renaming a note preserves its behavior, and removing a directive disables it. New `donate` and `kmp` notes are ordinary notes unless their bodies contain directives.

Create/edit commands validate markup before saving. Invalid markup cannot replace a note. Raw database reads return the stored body without rendering or shuffling. Legacy literal `\n` / `\r\n` separators are normalized outside JSON strings; JSON escapes stay intact.

## Grammar

Only top-level backtick fences with the exact info string `random`, `faq` or `components-v2` are directives.

- Use at least three backticks. The closing fence needs at least as many, with no non-whitespace suffix.
- Up to three leading spaces work. Directives must close.
- Tilde fences, indented code, blockquotes, list contents, unknown fence languages and examples inside ordinary code fences remain literal Markdown.
- Use a longer outer fence when an example contains backtick fences.
- Multiple random groups are allowed. Each note may have only one rich block: FAQ or Components V2.
- JSON must contain one valid value, valid UTF-8 and paired Unicode surrogate escapes. Unknown fields, unsupported types and empty blocks are rejected.

## Random groups

Each retrieval shuffles whole entries, joins them with newlines and leaves surrounding Markdown fixed. Entries must be nonempty Markdown strings. They are not recursively evaluated as directives.

````markdown
Project links:
```random
[
  "Source: https://github.com/MetrolistGroup/Metrolist",
  "Website: https://metrolist.cc"
]
```
````

## FAQ

Use an optional `#` title and introduction, then top-level `##` questions with nonempty answers. Headings inside code, quotes or lists do not create questions. Other heading levels and setext headings remain answer text. Without an introduction, the default header is `# FAQ`.

````markdown
```faq
# Release FAQ

## Where are official releases?
https://github.com/MetrolistGroup/Metrolist/releases

## What should a bug report include?
Your app/build version, platform, exact error and steps to reproduce.
```
````

Discord shows a question selector and one answer at a time. Telegram and AI receive the full readable FAQ.

New FAQs need 1–25 questions. Selector labels/previews are shortened to Discord's 100 UTF-16-unit option limit; displayed answers are not. Each page, including surrounding Markdown, must fit the combined 4,000 UTF-16-unit text limit. Oversized migrated FAQs use readable fallback delivery.

## Components V2

The JSON object contains exactly `components`, an array of 1–10 supported components. The bot places them in a plain container; arbitrary Discord payloads, custom IDs, actions, JavaScript and colors are not accepted.

| Type | Required fields |
| --- | --- |
| `text` | Nonempty Markdown `content` |
| `separator` | No other fields |
| `section` | Nonempty `content` and HTTPS `thumbnail` |
| `gallery` | `urls`: 1–10 HTTPS media URLs |
| `buttons` | `buttons`: 1–5 objects with `label` and HTTPS `url` |

Button labels need 1–80 UTF-16 units. URLs must be absolute HTTPS, without credentials or whitespace, and at most 512 UTF-16 units. Only the listed fields are accepted.

````markdown
```components-v2
{
  "components": [
    {"type": "text", "content": "# Metrolist"},
    {"type": "separator"},
    {"type": "section", "content": "Official project links", "thumbnail": "https://github.com/MetrolistGroup.png"},
    {"type": "buttons", "buttons": [
      {"label": "Releases", "url": "https://github.com/MetrolistGroup/Metrolist/releases"},
      {"label": "Source", "url": "https://github.com/MetrolistGroup/Metrolist"}
    ]}
  ]
}
```
````

A complete message may contain at most 10 direct container children and 40 total components, including surrounding text and nested children. Combined displayed text must fit 4,000 UTF-16 units. A donor-welcome prefix counts toward delivery limits.

## Delivery

Discord note commands, triggers, donor welcomes and support use the authored layout regardless of name. Rich notes suppress mentions; donor welcomes allow their intended recipient's ping. Plain slash notes retain their existing user-mention behavior, without role/everyone pings.

Telegram and AI receive readable Markdown: shuffled entries, all FAQ answers, layout text and labeled media/button URLs. Separators become `---`.

Long ordinary notes are sent in full as `note.md`: Discord's limit is 2,000 UTF-16 units; Telegram's is 4,096, also checked against serialized HTML. These delivery limits do not block saving a plain note.

If a rich layout exceeds a limit or Discord rejects it, delivery retries as readable text or a full Markdown attachment. Visibility, reply and mention restrictions are preserved. Nothing is truncated.

Old `note:kmp:question` selectors remain usable while that note contains a FAQ. New selector IDs are bounded hashes. Deleting, renaming or removing a FAQ makes old selectors unavailable rather than restoring name-based behavior.

## Legacy migration

The first database open records `note_markup_v1` in `migrations`, in the same transaction as these changes:

- Existing `donate`: keep its first line fixed and wrap remaining nonempty entries in one random group, preserving rendered text and escaping.
- Existing `kmp`: wrap its Markdown in a safely sized FAQ fence. Longer fences preserve directive examples inside a legacy FAQ.
- Other legacy bodies, and ambiguous `donate`/`kmp` bodies containing top-level directives, stay literal through storage metadata. Valid JSON alone does not establish that a legacy example was authored markup.
- Invalid UTF-8 bodies stay byte-for-byte intact until an admin repairs them.

A successful validated edit activates markup on a literal legacy note. Put examples inside a longer ordinary fence if they must remain literal. Renaming alone does not activate markup.

The marker is written even on an empty database. Restarts never repeat the migration, including after directives are removed or an old name is reused. Failure rolls back bodies, interpretation metadata and marker together. Preserve all of them when upgrading.
