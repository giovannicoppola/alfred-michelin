# Differential scraper handoff

Context dump for a future Claude instance picking up this work. Current
date at handoff: 2026-04-22. Branch: `dev-scraper-resilience` (not yet
committed).

## What exists and works

A new `scrape-diff` subcommand for the bundled Michelin scraper that
refreshes the DB for a target year without downloading every detail
page. Verified end-to-end by a smoke test against the post-2026 DB with
`-year 2027`: 19,081 award rows written, 1,508 restaurants flipped to
`in_guide=false`, only 5 detail pages fetched (~13 seconds wall time
against a warm colly cache).

Entry point:

```
cd "database builder/michelin-my-maps-3.2.1"
./mym scrape-diff -year 2027
```

## Files touched

All paths relative to `database builder/michelin-my-maps-3.2.1/`:

| File | Change |
| --- | --- |
| `internal/scraper/diff.go` | **NEW**. `RunDiff`, `setupDiffMainHandlers`, `writeListingOnlyAward`, `sweepDroppedRestaurants`, `buildDistinctionResolver`. |
| `internal/scraper/extract.go` | Added `diff_target_year` context-key override. Sits after the `publishedYear` extraction — the override wins. |
| `internal/storage/sqlite.go` | Added `LatestAwardInfo`, `GetLatestAwardsByURL` (single correlated query, returns `map[url]LatestAwardInfo`), `SetInGuide` (column-only update). |
| `cmd/mym/mym.go` | Added `scrape-diff` subcommand case + `handleScrapeDiff` function. |

Also modified earlier in this branch (unrelated to diff but shipped
together):

- `internal/webclient/webclient.go` — `DomainGlob` on `LimitRule`,
  `Clone()` re-applies limit, `ClearCacheByURL` helper.
- `internal/scraper/scraper.go` — multi-pass retry loop, WAF challenge
  detection, backoff with jitter.
- `internal/models/award.go` — price validator relaxed.
- `database builder/location_tool/zipcode_location_columns.go` — three
  patches to cope with 2026 address format (state abbrev included),
  false-positive `isUSA` on mid-word "usa", and single-token locations.

## How the diff pass decides what to do

Per unique URL discovered in a listing walk:

1. **Not in DB** → fetch detail, insert. Counter: `newCount`.
2. **In DB, distinction differs** → fetch detail, upsert award. Counter:
   `awardChanged`.
3. **In DB, latest award year ≥ target year** → no-op (URL is still
   marked `seen` so the sweep leaves `in_guide` alone). Counter:
   `alreadyCurrent`.
4. **In DB, distinction matches, no row for target year** → write a
   listing-only award row cloning `price` and `greenStar` from the most
   recent award. Counter: `unchanged`.
5. **In DB but never surfaces in any listing** → post-walk sweep flips
   `in_guide=false`. Counter: `dropped`.

URL dedup is load-bearing. `state.seen` is a `sync.Map` and we use
`LoadOrStore` so the handler early-returns on duplicate listing events
(pagination and cross-listing surface the same URL multiple times).
Without this, counters over-report by ~9% even though `SaveAward` is
idempotent. See `diff.go:161`.

## Invariants you must preserve if you refactor

- **`diff_target_year` context-key wins over `publishedYear`.** The
  detail page's JSON-LD tells you Michelin's current release year, not
  the year the user passed with `-year`. `extract.go:47-52` enforces
  this. If you move year extraction around, keep the override.
- **`SetInGuide` must not touch other columns.** The restaurant
  validator rejects rows with empty fields the diff pass doesn't
  refetch. `SetInGuide` uses `UpdateColumn` specifically to bypass
  `BeforeUpdate`. Don't switch it to `Save()`.
- **Type-assertion to `*storage.SQLiteRepository`.** Diff mode reads via
  a raw SQL correlated query that isn't on the generic
  `RestaurantRepository` interface. `RunDiff` fails fast if the repo
  isn't the concrete SQLite type. If we ever add a second backend,
  either lift `GetLatestAwardsByURL` / `SetInGuide` onto the interface
  or keep the type assertion.
- **Single listing pass, no multi-pass retry loop.** Unlike the full
  scraper, diff runs a single pass. The assumption is that diff pops
  are small enough that users can re-run on transient failure — don't
  port over the retry-pass machinery without thinking about what that
  means for listing-only clones of failed URLs.

## What's untested / risky

- **Year-over-year price changes on unchanged distinctions.** If
  Michelin updates a restaurant's price range but keeps its
  distinction, the diff clones the old price. Price changes are rare,
  so this is considered acceptable; a full rescrape every 2–3 years
  reconciles. If that's not acceptable, widen the "fetch detail page"
  branch to include price mismatches — but listing pages don't expose
  price, so this would require either a heuristic or always fetching.
- **Michelin rename of a starter URL.** `buildDistinctionResolver`
  matches by the trailing path segment (e.g., `1-star-michelin`). If
  Michelin restructures the URL, distinction comes back as `""` and
  the code falls back to fetching the detail page for every listing
  event in that segment — slow but correct.
- **Real-world 2027 run not done.** Smoke test was synthetic (`-year
  2027` against the 2026 DB, so every restaurant hit the
  "unchanged → clone forward" branch). The `award_changed` and `new`
  branches were exercised by 5 URLs only. If Michelin 2027 is
  structurally different (new listing HTML, renamed distinctions),
  things may break in ways the smoke test didn't surface.

## Tooling and shell notes

- User's zsh has widgets that hijack `cd` inside the Bash tool (fails
  with `command not found: z`). Workarounds: prefix commands with
  `bash -c '...'`, or use `go build -C <dir>` / `go run -C <dir>`
  flags to avoid `cd` entirely.
- `sqlite3` is on `PATH`, safe to use directly.
- User prefers tight responses. Don't narrate; state results. See
  `MEMORY.md` if it exists for anything else.

## DB state at handoff

`data/michelin.db` is at the canonical post-2026-refresh state:

- 23,517 restaurants, 76,404 awards, 0 for year=2027, 2,929 with
  `in_guide=false`.
- Backups in `data/`:
  - `michelin.db.pre-refresh-backup` — pre-2026 scrape
  - `michelin.db.pre-location-tool-backup` — post-scrape, pre-location-tool
  - `michelin.db.pre-diff-smoketest` — current canonical state (copy used
    to restore after smoke tests)

## Next steps if asked

- Commit the branch. Git status at handoff: modified `images/icon_builder.key`
  and `source/info.plist` (pre-existing, unrelated); uncommitted new files
  in `database builder/michelin-my-maps-3.2.1/internal/scraper/diff.go`
  and friends.
- Run against Michelin 2027 when it drops (historically late April /
  early May for parts of Europe; full guide rolls through the year).
  Back up `michelin.db` first, then `./mym scrape-diff -year 2027`.
- Consider lifting `GetLatestAwardsByURL` / `SetInGuide` onto the
  `RestaurantRepository` interface if anyone adds a second backend.
- `pkg/db/database.go:1031` still hard-codes
  `/Users/giovanni/gDrive/GitHub repos/alfred-michelin/source`. Harmless
  at runtime but worth cleaning up.

## Related memory / context

See `2026-refresh-report.md` in this repo for the full 2026 scrape
report (runtime, warnings, deltas, location-tool notes) — the diff
scraper section there is a shorter restatement of this document.
