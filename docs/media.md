# Media commands

## Nuke GIF

- `/nuke user:@someone` sends an animated cartoon using that member's avatar and server name.
- Reply to a message with `garmin nuke them` (case-insensitive; `garmin,` and `ok garmin` also work).
- The GIF shows a falling bomb, nuclear blast, cartoon blood splatter and aftermath. This is only an image effect: no ban, timeout, message deletion or other moderation action occurs.
- Rendering runs locally without external assets, AI calls or extra programs. One nuke renders at a time. A failed avatar download uses a placeholder. The 480×320, 48-frame animation loops.

## OCR Translate

Right-click a message, then select **Apps > OCR Translate**. It reads attached or embedded images and returns the original text plus an English translation. Text-only messages can also be translated. Results are visible only to the person invoking the action; long results are attached as `translation.txt`.

The action uses the existing configured vision model through OpenRouter. Selected images/text are sent to that provider, without chat history, shared memory, tools or nearby messages. Results are not added to the bot's conversation memory. The existing AI concurrency limit and per-user cooldown apply. Up to three images are accepted. Output-limit truncation is explicitly marked as a partial result; use a smaller crop for dense pages. GIFs use a representative middle frame, not every frame. Small or blurry text can be misread; unreadable content should be marked rather than guessed.

## SynthID research

No SynthID verdict action is currently shipped. No validated, general-purpose offline SynthID image decoder was found in the reviewed sources:

- Google's open-source [SynthID Text](https://github.com/google-deepmind/synthid-text) detects text watermarks, not image watermarks.
- [Rinne414/SynthID-detector](https://github.com/Rinne414/SynthID-detector) explicitly reports that its watermark-template detector failed on new images: 0 of 33 held-out AI images detected. Its replacement generic AI-image classifier is not a SynthID decoder.
- [reverse-SynthID](https://github.com/aloshdenny/reverse-SynthID) offers model/resolution-specific spectral heuristics, not Google's decoder. Its claimed accuracy is not sufficient evidence for a general watermark verdict on Discord images.
- [ai-watermark-detector](https://github.com/cpeoples/ai-watermark-detector) labels its SynthID image heuristic as non-forensic. C2PA provenance checks are distinct from detecting a pixel watermark.

Google's [official SynthID tools](https://deepmind.google/models/synthid/) provide hosted verification. Neither an AI-image classification nor missing metadata proves whether a SynthID watermark is present. Do not present an unvalidated offline score as a verified positive or negative result.
