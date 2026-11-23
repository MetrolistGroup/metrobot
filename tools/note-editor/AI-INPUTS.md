# AI input map

Metrobot does not discover or load `AGENTS.md`, `SOUL.md`, or `IDENTITY.md`. This file maps every real AI prompt, skill, and source input in the repository.

## Editable inputs

- **Chat system prompt.** Reads SQLite setting `garmin_base_system_prompt`, falling back to `cmd.GarminSystemPrompt()` outside `#general`, support, routing, and OCR.
- **Shared memory.** Reads the live `garmin-memory.md` file in ordinary chat, capped at 16 KiB including the trailing newline, and excludes it from `#app-support`.
- **Skills and guides.** Embeds nine Markdown files from `discord/garmin_skills/*.md` covering `metrolist`, `support`, and seven on-demand setup guides. Changes require a rebuild.
- **Go source strings.** Compiled prompts, schemas, context wrappers, and error strings live in the files below. Editing them validates Go syntax in the editor and requires a build, test run, and redeploy to take effect.

## Prompts, routing, and OCR

| Source | AI-facing content |
| --- | --- |
| `cmd/garmin_prompt.go` | Chat and `#general` personas, reply style, Discord rules, safety policy, and web lookup rule |
| `cmd/garmin_reply.go` | Embed and `components-v2` format instructions and validation |
| `discord/garmin_support.go` | `#app-support` system prompt and legacy unchecked triage prompt |
| `discord/support_decisions.go` | Automatic support eligibility, routing criteria, and note catalog assembly |
| `discord/support_answer.go` | Evidence checking rules, defect taxonomy, and rewrite hints |
| `discord/support_excerpt.go` | Verbatim FAQ section selector prompt and thresholds |
| `discord/support_learning.go` | Solved-problem confirmation check and `confirmed-fixes` formatting |
| `discord/support_images.go` | Support screenshot OCR transcription prompt and `[Text read from the attached image]` header |
| `cmd/chat_decisions.go` | Qwen chat-completion decision system prompt and `submit_decisions` tool schema |
| `discord/ocr.go` | Message and fallback text OCR/translation prompt |
| `discord/ocr_image.go` | In-place JSON bounding-box image translation prompt |

## Runtime assembly and context

| Source | Role |
| --- | --- |
| `discord/bot.go` | OpenRouter client, Decisions client, and shared memory initialization |
| `discord/garmin_agent.go` | Request orchestration, tool-round budgets, privacy guards, nudges, and retries |
| `discord/garmin_prompt.go` | Base prompt selection, project reference wrapper, and shared memory wrapper |
| `discord/garmin_profile.go` | Discord member, role, pronoun, channel context, and timestamp-only support context |
| `discord/garmin_context.go` | 12-message sliding conversation window, omission marker, and context cutoffs |
| `discord/support_session.go` | 3-minute support sessions and 20-minute human handoffs |
| `discord/garmin_input.go`, `discord/garmin_emoji.go`, `discord/garmin_gif.go` | Message text, attachments, custom emoji images, and GIF handling |
| `discord/garmin_retrieval.go` | Device source triggers, web query bounds, and URL grounding helpers |
| `discord/garmin_history.go` | Channel history and approved community channel message formatting |
| `discord/support_context.go` | Embedded support core and on-demand guide loaders |
| `cmd/ai.go` | OpenRouter chat completion client, token caps, and runtime model identity injection |
| `cmd/decisions.go`, `cmd/openrouter.go` | Decisions HTTP client, model remapping, reasoning defaults, and provider preferences |

## Tools and live data

- `discord/garmin_tools.go` defines every tool name, JSON schema, availability rule, and error wrapper.
- `cmd/notes.go` and `internal/notefmt/*.go` render retrieved notes for AI and platform delivery.
- `github/*.go`, `firecrawl/*.go`, `gsmarena/*.go`, `nanoreview/*.go`, `technicalcity/*.go`, and `cmd/calculator.go` produce live tool outputs.

## Request-only data

Live Discord messages, recent conversation windows, channel history fetches, attachments, and tool responses exist per request and are not saved as editable baselines. Editor exports exclude credentials, private logs, hidden reasoning, and conversation RAM.
