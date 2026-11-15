# AGENTS.md and every AI input source

## There is no runtime AGENTS.md

Metrobot does **not** discover or load `AGENTS.md`, `SOUL.md` or `IDENTITY.md`. None was found in the deployed runtime or its checked parent directories. This is an input map, not an instruction file sent to the bot. Edit the actual prompt, guide or memory documents listed below. Creating an unused AGENTS.md would not change AI behavior.

## Editable deployed text

- **Chat system prompt**: the effective `garmin_base_system_prompt` SQLite setting. If no override exists, the editor shows the built-in `cmd.GarminSystemPrompt()` text. Saving an edit stages a new override, not a note. This affects ordinary chat, not #general, support, routing or OCR.
- **Shared memory**: the live configured `garmin-memory.md`, not the repository seed. It is background data for ordinary chat and is omitted from support. The limit is 16 KiB including the final newline.
- **Skills and guides**: all nine Markdown files embedded from `discord/garmin_skills/*.md`. They include the project reference, support core and seven on-demand guides. The project core precedes the on-demand details heading. Editing these requires rebuilding the binary.
- **Built-in instructions and templates**: the Go sources below are the exact deployed revision. Edit text inside the source strings. The right-hand pane decodes every string literal, including JSON schemas and labels, for inspection. These lists are deliberately a superset: imports, errors and code-only strings are not necessarily AI input. Conditional code and concatenation determine the actual request. Saving validates Go syntax; deployment still requires build/tests.

## Built-in prompts, rules and routing

| Source | AI-facing text |
| --- | --- |
| `cmd/garmin_prompt.go` | Chat and #general personas, shared reply style, Discord rules, safety/authority policy and web-lookup rule |
| `cmd/garmin_reply.go` | Optional embed-format instructions and example |
| `discord/garmin_support.go` | Dedicated support system prompt, legacy triage prompt and triage tools |
| `discord/support_decisions.go` | Should-answer and routing instructions, criteria, options and note-catalog formatting |
| `discord/support_answer.go` | Evidence-check instructions, defect criteria and rewrite hints |
| `discord/support_excerpt.go` | Verbatim note-section selection instructions and options |
| `discord/support_learning.go` | Solved-problem confirmation instructions and confirmed-fix note formatting |
| `cmd/chat_decisions.go` | Chat-based decision system prompt, response tool schema, evidence instructions and submitted state |
| `discord/ocr.go` | Image transcription/translation instructions and message/image assembly |

## Request-specific instructions and context

| Source | AI-facing text |
| --- | --- |
| `discord/garmin_agent.go` | Runtime retrieval/privacy guidance, channel-specific instructions, memory wrapper, request time, required-source nudges, final-answer nudges, evidence-check rewrite requests, image/tool messages and conditional nightly guidance |
| `discord/garmin_prompt.go` | Base-prompt selection, verified-project wrapper and legacy shared-memory wrapper |
| `discord/garmin_profile.go` | Discord context wrapper, channel descriptions, speaker/profile/role/pronoun fields, reply context and timestamps; support receives timestamp-only context |
| `discord/garmin_context.go` | Retained conversation, omission marker and context bounds; old summaries are removed rather than sent as new authoritative evidence |
| `discord/garmin_input.go`, `discord/garmin_emoji.go`, `discord/garmin_gif.go` | User text, scoped images, attachment/emoji metadata and GIF visual input |
| `discord/garmin_retrieval.go` | Web-result instructions, lookup context and source-selection helpers |
| `discord/garmin_history.go` | Retrieved channel history, participant names, timestamps, reply links, attachment metadata and coverage limits |
| `discord/support_context.go` | Embedded skill/guide loading and short-project-reference composition |
| `cmd/ai.go` | Final system/context/message/tool serialization and provider-facing runtime identity |
| `cmd/decisions.go`, `cmd/openrouter.go` | Decision payload and provider request configuration |

## Tools and retrieved evidence

- `discord/garmin_tools.go`: every tool name, description, JSON parameter schema, tool-result wrapper/error and tool availability policy.
- `cmd/notes.go` and `internal/notefmt/*.go`: note retrieval, authored random/FAQ/Components V2 rendering and readable fallback. Actual stored note bodies/descriptions are in the **Saved notes** category, fetched from production.
- `github/*.go`: live project/releases/commits/issues/code/repository/user tool results and their labels/coverage/error text.
- `firecrawl/*.go`: public web search/page content and source formatting.
- `gsmarena/*.go`, `nanoreview/*.go`, `technicalcity/*.go`: live device data and source/result formatting.
- `cmd/calculator.go`: arithmetic results and errors.
- Remaining `discord/garmin*.go` and `discord/support*.go` files are included for completeness, including context, delivery and lookup helpers. Their incidental code strings are not all sent to a model.

## What cannot be a saved editable document

The **current Discord message**, retained conversation, referenced messages, current names/roles/channel topics, live timestamps, actual attachment/image contents, public pages and API/tool results depend on a specific request. There is no single permanent "all text" value. Their schemas, templates and producers are included above; real future values are not fabricated or frozen into instructions.

This editor does **not** collect conversation RAM, private channel logs, credentials, provider keys, hidden model reasoning or arbitrary API responses. Stored shared memory is fetched explicitly because it is an authored AI input. Tool data can be private when retrieved by an authorized request; copying it into a public file or unconditional prompt would break its scope.

## Applying edits later

All saves remain local. Note drafts and AI-text drafts have separate immutable baselines. Prompt overrides go to their SQLite setting; shared memory goes to its configured file; embedded guides and Go text need a source build/redeployment. The AGENTS.md input map is reference-only and has no upload destination.

Before applying, compare edited destinations with their baselines, preserve unrelated live bot data, back up production, validate changed Go sources, build and test. If the prompt override and its built-in fallback were both edited, resolve which should be active rather than silently ignoring one. Runtime restarts clear in-memory conversation state. Never install a snapshot database over the live database.
