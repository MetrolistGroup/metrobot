# Dashboard assets

The dashboard is dark-only and has no build step or network dependencies at runtime. BeerCSS supplies real `field border` input/select/textarea wrappers, `button` / `border no-round` controls, wrapping `nav` groups and a `table.border.medium-space`. `style.css` overrides the theme with charcoal, teal and stone, square corners, system fonts, no shadows or animations. Both the color-scheme meta and CSS are dark; the body uses `class="dark"`.

## Server integration

All files stay flat in `dashboard/static/`, covered by the existing embed. In addition to `/`, `/app.js` and `/style.css`, the server's explicit GET asset whitelist must serve:

- `/beer.min.css` (`text/css`)
- `/prism.min.js` (`text/javascript`)
- `/editors.js` (`text/javascript`)
- `/archives.js` (`text/javascript`)

The two `*-LICENSE.txt` files are retained with the vendored sources; they need not be public routes. No BeerJS, dynamic-color library, fonts, icon files, workers or autoload plugins are needed. Keep the existing self-only CSP without `unsafe-inline`: there are no inline scripts/styles, external fonts or JS-injected styles. Links to Discord and attachments are user-activated HTTPS links, not embedded media.

## Pinned dependencies

### BeerCSS 5.0.3 (MIT)

- Official repository: https://github.com/beercss/beercss
- Official docs consulted: `docs/INDEX.md`, `SETTINGS.md`, `INPUT.md`, `TEXTAREA.md`, `BUTTON.md`, `NAVIGATION.md`, `TABS.md`, `TABLE.md`, `SELECT.md` on the official main branch (the examples pin 5.0.3).
- Source: https://registry.npmjs.org/beercss/-/beercss-5.0.3.tgz
- NPM release git revision: `b2a24300f980d64a7dbafa905be6d5c191f160e5`
- Input file: `package/dist/cdn/beer.min.css`
- Input SHA-256: `9415a6ee147efc95a8713e2c1a3c8c387c7f78c6b34ada6dc309ef0d18f34de7`
- Local `beer.min.css` SHA-256: `6e438d054066af65e4f1bc99829c41f934f90e97dfa2b8b0abe0d102473cd2c0`
- License: `dashboard/static/beer-LICENSE.txt`, SHA-256 `cc7f9f4d816f02e7e909c18d1224af1c242ca4a2e2dc3ff008d165a775e21426`

Modification: remove exactly the four unused `@font-face{…}` rules (Material Symbols, including their CDN fallbacks), and prepend the provenance comment. All other vendor CSS is unchanged. Icon-dependent widgets and optional SVG shape helpers are not used; the vendor image variable is overridden to `none`. Custom CSS overrides the default font family, so no font download is needed. Dark mode is CSS-only, as documented by BeerCSS.

### PrismJS 1.30.0 (MIT)

The user requested mature syntax highlighting for Markdown and custom note fences; Prism provides escaped tokenization without an editor framework or frontend toolchain.

- Official repository: https://github.com/PrismJS/prism
- Source: https://registry.npmjs.org/prismjs/-/prismjs-1.30.0.tgz
- Local `prism.min.js` contains the following unchanged minified components, in order, separated by newlines:

| Input under `package/components/` | SHA-256 |
| --- | --- |
| `prism-core.min.js` | `6caad316dd991f24f8004e0b9c19c055cb5829ff65e973fbee406f96d81b8e7e` |
| `prism-markup.min.js` | `879fc9d256c352d980e053857fa707330853b8bfb67ce284ea661a24dec5756e` |
| `prism-markdown.min.js` | `9f1166a087d9a9ffb3a833f2bccbe00920b55b41ade02a0b3054b7ab5fbc70ea` |
| `prism-json.min.js` | `956d86baa5ae7ec4106758f354ac2d140bdcd7fc103dece02f73ed12b8d663e4` |

- Prepended: provenance comment and `window.Prism = { manual: true };` before core executes, to prevent automatic DOM highlighting. No autoloader or network plugin is included.
- Local combined SHA-256: `10ae37fd8010c94ba7210f017f96abd27fb26015aa6b3844451a985592356b5f`
- License: `dashboard/static/prism-LICENSE.txt`, SHA-256 `2b947f0901a7ffcf08a89957da9783c0e9c6e72cb6ce8e959f501ab5409e4d2b`

`editors.js` aliases `random` and `components-v2` to JSON and `faq` to Markdown. Prism's Markdown fence hook highlights those block bodies with the matching grammar. Its returned escaped spans are the only `innerHTML` assignment; API data elsewhere uses `textContent`.

## Editors and resource scope

Notes, shared memory and the Garmin prompt use a native labeled textarea over an aria-hidden highlighted pre. They share font, line height, padding, tab size, wrapping, gutter and scroll offsets. Highlight tokens change color only to keep positions aligned. Without Prism, the textarea remains ordinary visible text. Forced-colors mode also uses native text. Tab inserts two spaces; Shift+Tab or Escape followed by Tab moves focus out. Example buttons insert at the cursor without overwriting any existing selection or draft.

Section changes retain all drafts in memory. Failed saves and session expiry retain unsaved text; the read-only recovery panel includes prompt drafts. Logout and page exit include prompt drafts in unsaved-change checks. Unchanged note bodies retain their original CRLFs when only metadata changes. Drafts are not stored in local/session storage.

Cases load lazily with no initial date filter. Discord history requires both inclusive UTC dates and only scans a bounded page per user action; the UI never automatically follows cursors. Filter changes abort and invalidate old requests. A cursor permits continuing even if that page returned zero matches. Warnings and incomplete searches remain explicit. Auth failures/logout clear archive results and cancel requests.

Prompt GET is lazy and permanent-admin-only. Prompt mutations reload only the prompt. Note/memory/admin mutations reload `/api/state`, not prompt/cases/logs, so unrelated prompt drafts and archive filters stay intact. Prompt reset discards its draft only after confirmation and successful mutation. The browser enforces nonblank text and a 64 KiB UTF-8 limit; the backend remains authoritative.

## Verification

No coded UI or DOM tests are added. Run `node --check dashboard/static/*.js` individually, `git diff --check`, and a non-DOM Prism tokenization/escaping self-check against Markdown, JSON and the custom fences.

Manual integrated checks after the asset routes and parallel APIs are installed:

1. Sign in as a regular and permanent admin; confirm only permanent admins see prompt/admin controls. Confirm local asset requests and no remote font/script requests under the current CSP.
2. Type Markdown and random/FAQ/layout fences, scroll/wrap/resize the source, select text and use keyboard navigation. Insert examples inside an existing draft. Rename a marked-up note; behavior must follow fences rather than its name.
3. Keep note/memory/prompt drafts while switching sections and saving an unrelated resource. Simulate failed saves and session expiry; only unsaved text should be recoverable, and fetched logs must be cleared. Logout must warn about prompt drafts.
4. Open Cases with blank dates; search by case/ID/reason/action and load another page. Search each log source for a historical single UTC day and a range. Change filters during a request; old responses must not appear. Continue a zero-match page with a cursor and inspect partial/complete and warning text.
5. Save/reset the base prompt with confirmation; test blank and >64 KiB drafts. Router, summarization and tool permissions remain outside this editor.

Browser layout and live API checks are intentionally left to integrated manual verification; this frontend-only lane does not change Go routes or API handlers.
