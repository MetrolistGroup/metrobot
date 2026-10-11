# Note markup

Notes are Markdown. Behavior belongs to their **body**, never their name. Rename a
note to keep its behavior; remove its directive to disable it. New `donate` and
`kmp` notes are ordinary notes unless their bodies contain directives.

The editor API returns the exact stored body, without rendering or shuffling.
Ordinary notes saved before this feature retain literal Markdown interpretation,
including top-level directive examples. Renaming them does not activate markup;
a successful validated edit does. New notes interpret directives immediately.
Create/edit commands and the dashboard share server-side validation. Invalid
markup cannot replace an existing note. Commands still accept legacy literal
`\n` / `\r\n` newline separators outside JSON strings; JSON string escapes are
left intact, including `\n`, `\\n`, escaped quotes and Unicode escapes.

## Grammar

Only **top-level backtick code fences** with the exact info string `random`,
`faq`, or `components-v2` are directives. Use three or more backticks; closing
fences have at least as many backticks and no non-whitespace suffix. Standard
Markdown indentation (up to three spaces) works. Directives must be closed.
Tilde fences, indented code, blockquotes, list contents and examples inside an
ordinary code fence remain literal Markdown. Unknown fence languages are literal.
Choose a longer outer fence when its body contains backtick fences.

Multiple random groups are allowed. At most **one** rich block (`faq` **or**
`components-v2`) is allowed per note. Markdown before and after it is retained,
including random groups. JSON must be one valid value with valid UTF-8 and paired
Unicode surrogate escapes. Unknown fields, unsupported types and empty blocks
are rejected. Random entries are Markdown, not recursively evaluated directives.

## Random groups

Each retrieval shuffles whole entries independently and joins them with a newline.
The surrounding Markdown stays fixed. Each entry must be a nonempty Markdown
string; entries can contain paragraphs and links.

````markdown
Support the people behind the app:
```random
[
  "Mo: https://buymeacoffee.com/mostafaalagamy",
  "Nyx: https://github.com/sponsors/nyxiereal\nIndependent app development."
]
```
Thank you for supporting open source.
````

## FAQ

Use an optional `#` title, optional introductory Markdown, then top-level `##`
questions followed by nonempty Markdown answers. Headings inside code, quotes or
lists do not create questions. Other heading levels and setext headings are
ordinary answer Markdown. Without introductory Markdown, the default header is
`# FAQ`. Discord adds “Choose a question below.” and a question selector, showing
one answer at a time. Telegram and AI receive the complete readable Markdown.

````markdown
These answers apply to the current app.
```faq
# Playback FAQ

## Playback stops. What should I try?
Disable battery restrictions for Metrolist, then restart the app.

## Where can I download releases?
Use https://github.com/MetrolistGroup/Metrolist/releases.
```
Still stuck? Include your app version when asking for help.
````

New FAQs need 1–25 questions. Selector labels and answer previews are safely
shortened to Discord's 100 UTF-16-unit option limit; the displayed answer is not
shortened. Every page, including the header and surrounding Markdown, must fit
Discord's combined 4,000 UTF-16-unit text limit. Existing migrated FAQs that do
not fit still deliver their full readable content through the fallback below.

## Authored Components V2

The JSON object has exactly one field, `components`, containing 1–10 components.
The server constructs typed Discord components in a default plain container.
It never accepts an arbitrary Discord payload, custom IDs, actions, JavaScript,
colors or raw configuration.

Supported schemas (all fields shown are required):

- `{"type":"text","content":"Markdown"}`: nonempty Markdown text.
- `{"type":"separator"}`: a divider.
- `{"type":"section","content":"Markdown","thumbnail":"https://…"}`:
  nonempty Markdown text with an HTTPS thumbnail.
- `{"type":"gallery","urls":["https://…"]}`: 1–10 HTTPS media URLs.
- `{"type":"buttons","buttons":[{"label":"Download","url":"https://…"}]}`:
  1–5 URL-only buttons; labels need 1–80 UTF-16 units.

URLs must be absolute HTTPS URLs without credentials or whitespace and at most
512 UTF-16 units. Only the listed fields are accepted for each component type.

````markdown
Official links:
```components-v2
{
  "components": [
    {"type": "text", "content": "# Metrolist\nMusic, without distractions."},
    {"type": "separator"},
    {
      "type": "section",
      "content": "Find the project and its latest releases.",
      "thumbnail": "https://github.com/MetrolistGroup.png"
    },
    {
      "type": "buttons",
      "buttons": [
        {"label": "Releases", "url": "https://github.com/MetrolistGroup/Metrolist/releases"},
        {"label": "Source", "url": "https://github.com/MetrolistGroup/Metrolist"}
      ]
    }
  ]
}
```
Use official releases when reporting a problem.
````

The complete message may have at most 10 direct container children (including
surrounding text) and 40 total components. Section text/thumbnail and button row
children count toward the total. Combined displayed text, including surrounding
Markdown, must fit 4,000 UTF-16 units. A donor-welcome prefix is counted at delivery;
if it pushes a valid layout over a limit, the readable fallback is used.

## Delivery and fallback

Discord note triggers, slash commands, donor welcomes and the app-support agent
use the authored layout independent of the note name. Slash commands retain their
public/private behavior. Rich notes and triggers suppress mentions; donor welcomes
allow only their intended recipient's ping. Plain slash notes retain their existing
user-mention behavior, without enabling role/everyone pings.

Telegram and AI see readable Markdown with no custom directive fences: random
entries in their retrieved order, all FAQ questions/answers, and layout text plus
thumbnail/gallery URLs and labeled button URLs. Separators become `---`.

If a rich layout cannot fit at delivery, the full readable note is sent as plain
text, or as `note.md` when the text plus any prefix exceeds 2,000 UTF-16 units.
Nothing is truncated. Oversized legacy FAQ pages or more than 25 legacy questions
fall back the same way. Already-posted `note:kmp:question` selectors remain usable
while the migrated `kmp` note still contains a FAQ; new FAQ IDs are bounded hashes,
so long Unicode names cannot exceed Discord's custom-ID limit. Removing the FAQ
or deleting/renaming a note makes an old selector unavailable rather than invoking
name-based behavior.

## One-time migration

On the first database open after this change, a transaction migrates saved legacy
notes and records `note_markup_v1` in the `migrations` table:

- Existing `donate`: keep the first line fixed and put the remaining lines in one
  JSON random group, using the former retrieval's newline normalization. Blank
  lines attach to a neighboring entry; rendered text and JSON escaping are preserved. A note without nonempty remaining entries stays literal.
- Existing `kmp`: wrap the complete original Markdown in a safely sized FAQ fence.
- Already directive-authored `donate`/`kmp` bodies are left unchanged.
- All other legacy bodies are unchanged and marked as literal Markdown using
  internal storage metadata. Both invalid and valid JSON directive examples stay
  literal, across renames and restarts. A successful validated edit activates
  markup; to keep examples literal when editing, put them inside a longer ordinary
  Markdown code fence, as in the examples above.
- Invalid UTF-8 legacy bodies are left byte-for-byte intact rather than silently
  replaced by JSON encoding; an admin must repair them before note validation.

The marker is written even on a new/empty database. Restarts never reapply the
migration, including after an admin removes directives or creates a new note with
a legacy name. A migration failure rolls back bodies, interpretation metadata,
and marker. Migration
runs only when the application opens its database; development and tests do not
require access to the live database or configuration.
