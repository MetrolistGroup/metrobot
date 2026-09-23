# NanoReview client

```go
client := nanoreview.New(database) // nil disables caching
matches, err := client.Search(ctx, "OnePlus 15", 10) // autocomplete metadata
phone, err := client.Lookup(ctx, "OnePlus 15")       // search then full specs
cpu, err := client.Device(ctx, "cpu/amd-ryzen-z1")
```

`Device.Slug` is category-qualified (`phone/oneplus-15`, not `oneplus-15`), so
component values remain unambiguous across phone, CPU, GPU, laptop and SoC pages.
`Device` also accepts `/en/category/slug` and full HTTPS NanoReview URLs. Plain
names belong in `Lookup` or `Search`; they are never converted to guessed slugs.
Search results have names, slugs and URLs; image, summary and specifications are
loaded only by `Device`/`Lookup`. `Lookup` selects the site's first search result.
Limits outside 1..25 use 25.

## Verified site behavior

Live `curl` inspection found the autocomplete endpoint in
`/assets/web/app.js?ver=S5090-turbo-GTS6`:

```
GET https://nanoreview.net/api/search?q=oneplus%2015&limit=25
```

The site's comparison inputs add `type=phone` (or `cpu`, `gpu`, `laptop`, `soc`).
Omitting `type` was verified to return cross-category results. The JSON array
contains `id`, `slug`, `label`, `name` and `content_type`; the client uses these
observed slugs, not guessed names. Search may return loosely related matches.

Basic curl requests returned Cloudflare 403, even for the OnePlus detail page.
With the browser User-Agent, Accept and Accept-Language headers used by this
client, search and all five detail categories returned HTTP 200. A live Go client
smoke check also successfully searched and parsed all five categories. The phone list
is not needed. Cloudflare restrictions can change: blocking, malformed responses
and missing specifications return errors, never fabricated results or cached
empty successes. There is no proxy service or external search-engine fallback.

The shared `.card` / `.specs-table` markup supplies specification and benchmark
sections. Laptop/phone configuration selectors are not executed; results describe
the page's default configuration. Query parameters for custom configurations and
comparison/list pages are deliberately unsupported. Only HTTPS `nanoreview.net`
product/API requests and safe same-origin redirects are permitted; bodies are
limited to 4 MiB and requests to 20 seconds. Images are metadata, never fetched.

Search arrays are cached for 24 hours, independently of requested limit; full
details for 7 days, using the existing database cache with `nano:search:` and
`nano:detail:` keys. Cache errors are returned. A nil DB performs uncached requests.

## Verification

`go test ./nanoreview`

`testdata/*.html` are reduced live-page excerpts (head metadata, product name,
image and one specification card), retaining actual table cells and inline markup:

- `/en/phone/oneplus-15`: Display, including its second table
- `/en/cpu/amd-ryzen-z1`: CPU
- `/en/gpu/geforce-mx230`: Memory
- `/en/laptop/apple-macbook-neo`: Case
- `/en/soc/qualcomm-snapdragon-425`: CPU

`search.json` combines one observed autocomplete record per category. Tests use
these fixtures and an in-process HTTP transport; they do not contact the site.
