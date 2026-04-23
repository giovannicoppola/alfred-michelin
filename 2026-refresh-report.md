# 2026 Refresh Report

## Overview

Full rescrape of `guide.michelin.com` to bring the bundled Michelin database
up to date with 2026 awards.

- **Branch:** `dev-scraper-resilience`
- **Start:** 2026-04-21 22:00:30 UTC
- **End:** 2026-04-22 19:17:28 UTC
- **Runtime:** ~21h 17min, single pass (`passes=1`, zero retry passes needed)
- **Source:** `guide.michelin.com` seeded from `models.GuideURL` (no external table input)

## Scrape summary

| Metric | Value |
| --- | --- |
| Restaurants upserted | 19,023 |
| Award rows dropped (empty price) | 57 → 0 (recovered — see below) |
| Request warnings | 9 |
| WAF challenges detected | 0 |
| Unresolved failures after final pass | 0 |

The 57 award drops during the main pass were validator rejections in
`models/award.go` — the restaurant row was saved but the award row was
skipped when Michelin's page omits the price range. Pre-existing behavior,
not caused by scraper changes. We relaxed the validator and reprocessed
those 57 URLs in a 0.6s targeted CSV pass; all award rows are now in the
DB (with `price=''`). Details in the follow-ups section below.

The 9 warnings came from a single Michelin-side throttling window
(2026-04-22 T02:08 → T08:11 UTC). No requests were permanently dropped;
colly's jittered exponential backoff absorbed them in the same pass.

## Database deltas

Comparison between `data/michelin.db.pre-refresh-backup` (pre-scrape) and
the newly written `data/michelin.db`:

| Table | Before | After | Δ |
| --- | ---: | ---: | ---: |
| `restaurants` | 21,092 | 23,517 | +2,425 |
| `restaurant_awards` | 68,813 | 76,404 | +7,591 |

### Awards by year

| Year | Before | After | Δ |
| --- | ---: | ---: | ---: |
| 2026 | 0 | 6,278 | +6,278 (new) |
| 2025 | 18,921 | 20,233 | +1,312 |
| 2024 | 18,337 | 18,338 | +1 |
| 2023 | 10,224 | 10,224 | 0 |
| 2022 | 9,324 | 9,324 | 0 |
| 2021 | 7,403 | 7,403 | 0 |

After counts include the empty-price retry pass described below. Of the 57
reprocessed URLs, 50 inserted new award rows and 7 updated existing rows
in place (the restaurant already had a 2026 or 2025 award row from the
main pass that the retry overwrote).

User tables (`user_favorites`, `user_visits`) are untouched by the scraper
and remain empty in all three DBs (new, backup, canonical).

## Scraper configuration used

| Setting | Value |
| --- | --- |
| `Delay` | 2s |
| `RandomDelay` | 3s (effective per-request wait: 2–5s) |
| `ThreadCount` | 2 |
| `MaxRetry` | 5 |
| `MaxRetryPasses` | 5 |

## Code changes merged into this refresh

Three files in `database builder/michelin-my-maps-3.2.1`:

- `internal/webclient/webclient.go` — `LimitRule` now has a proper
  `DomainGlob`, so the configured delay is actually enforced. `Clone()`
  re-applies the limit rule (colly doesn't carry it over), so the detail
  collector is rate-limited too. Added `ClearCacheByURL` for retry pass cache invalidation.
- `internal/scraper/scraper.go` — multi-pass retry loop, AWS WAF challenge
  detection (body-substring heuristic), jittered exponential backoff,
  429 treated like 403, failures recorded with preserved context keys for
  re-queueing across passes.
- `internal/models/award.go` — dropped the "price cannot be empty"
  validator rejection. Empty price is now accepted as "unknown" so newly
  promoted restaurants whose Michelin page lacks a price range still get
  their award row persisted. Year and distinction are still validated.

## Green-star capture fix

During validation of the report generator, the Green Star count for 2026
came back at **0** even though Michelin lists hundreds of green-star
recipients. Root cause: Michelin's 2026 template rework replaced the
server-rendered `"MICHELIN Green Star"` text block with a client-side
Mustache template, leaving only an HTML data attribute as the
server-side signal:

```html
<button … data-distinction="THREE_STARS" data-green-star="true" data-is-detail="true">
```

The old scraper XPath `//*[contains(text(),'MICHELIN Green Star')]`
relied on the text node and silently stopped matching, so the full
2026 scrape wrote `green_star=0` for every restaurant — even obvious
recipients like Arpège or Noma.

Three-file fix on `dev-scraper-resilience`:

- `internal/scraper/xpath.go` — XPath is now
  `//*[@data-green-star='true']`, matching the attribute directly.
- `internal/scraper/extract.go` — switched from `ChildText` to
  `ChildAttr(restaurantGreenStarXPath, "data-green-star")`.
- `internal/parser/parser.go` — `ParseGreenStar` accepts both
  `"michelin green star"` (unchanged for backfill / historical code
  paths) and `"true"` (the new attribute value).

Verified with a 4-URL smoke test: Arpège and La Marine flipped from 0
to 1 for 2026; Benu and Somni (both 3-star non-green-star) correctly
stayed at 0.

### Targeted rescrape

A two-phase CSV rescrape was run afterwards:

1. **Fast pass (546 URLs, ~7 min):** every restaurant that had ever
   held a green star at any point in the DB's history. This catches
   continuation — restaurants that kept their green star in 2026.
2. **Full pass (6,278 URLs):** every restaurant with a 2026 award
   row. Ran nearly instantly because colly's on-disk HTTP cache from
   the original April 2026 scrape was still warm — the attribute was
   there in the cached HTML all along, the old XPath just didn't know
   how to read it. This closes the "first-time green-star recipient"
   gap that the fast pass couldn't.

Results:

| Metric | Before fix | After fast pass | After full pass |
| --- | ---: | ---: | ---: |
| Distinct in-guide green-star restaurants | 546 | 556 | **588** |
| Current green-star (2025 or 2026) | 191 | 534 | **566** |
| Green-star rows for 2026 | 0 | 168 | **200** |
| Green-star rows for 2025 | 191 | 526 | 526 |
| Green-star rows for 2024 | 441 | 445 | 445 |

The 2025 row count jumped (191 → 526) because many regions
(particularly the US) still expose their current distinction under
year=2025 on Michelin's site, so the rescrape updates the 2025 row
rather than creating a new 2026 one. The +32 delta between fast and
full pass is Michelin's first-time 2026 green-star class that wasn't
on the DB's prior green-star list.

One URL failed during the fast pass: the old scraper had cached
`…/zurich/restaurant/elmira`, which now returns 404 (delisted).
Acceptable loss.

Pre-fix backup retained at `data/michelin.db.pre-greenstar-pass`;
pre-full-pass backup at `data/michelin.db.pre-full-greenstar-pass`.

## Country / US-state backfill

The scraper's upstream `Restaurant` model has no `country` or `us_state`
columns — those were added in the 2025 run by a post-processing tool
(`database builder/location_tool/zipcode_location_columns.go`) that
derives them from `address` and `location`. The 2026 refresh produced a
schema-identical-to-upstream DB missing both columns, so the tool was
re-run against the new DB.

Re-running against 2026 data exposed three format changes since 2025
that broke the original derivation; the tool was patched for each:

1. **Address now includes the state abbreviation.** Old layout
   `Street, City, ZIP, USA` became `Street, City, ST, ZIP, USA`. The
   tool hard-coded the ZIP at comma-index 2, so it read `"CA"` as the
   ZIP and returned no state. Patched to scan every comma-separated
   part: prefer an explicit 2-letter US state token, fall back to a
   strict 5-digit ZIP match.
2. **Naive `isUSA` substring matched mid-word.** `strings.Contains(addr, "usa")`
   flagged Japanese tokens like `Kusagawacho` (Kyoto) as US addresses.
   Tightened to compare the last comma-separated segment of location /
   address against an explicit US-token set (`"usa"`,
   `"united states"`).
3. **Single-token locations had no derivable country.** Michelin's
   location field is a bare city name for city-states (Dubai, Singapore,
   Macau, Hong Kong) and many single-city entries (Paris, London,
   Luxembourg). Added a `cityToCountry` fallback map for the
   city-states, then a final fallback to the address's last segment,
   which Michelin consistently populates with the country.

### Results

| Metric | Value |
| --- | --- |
| Restaurants with country | 23,517 / 23,517 (100%) |
| Distinct countries | 53 (after duplicate-casing merges — see below) |
| US restaurants | 2,314 |
| US restaurants with state | 2,314 / 2,314 (100%) |

Top countries: France (3,845), USA (2,314), Italy (2,314), Spain (1,600),
Japan (1,590), Germany (1,504), United Kingdom (1,391). Top US states:
CA (703), NY (598), FL (195), IL (186), DC (145), TX (140).

Columns were added via `ALTER TABLE restaurants ADD COLUMN country TEXT`
and `ADD COLUMN us_state TEXT` before the tool ran. A pre-tool backup
(`michelin.db.pre-location-tool-backup`) was taken in case of any
regression.

### Country-casing cleanup

The initial location-tool output only capitalized the first letter of
the country string ("United kingdom", "South korea", "Chinese mainland")
and produced duplicates when the same country surfaced via different
code paths (e.g. "Hong Kong" vs "Hong kong sar china"). The
`capitalize` helper in `zipcode_location_columns.go` now does per-word
title-casing with an abbreviation allowlist (`USA`, `UAE`, `UK`, `SAR`)
and lowercase connectors (`of`, `and`, `the`). Existing DB values were
normalized in place; four pairs of duplicates were merged to the
canonical form:

| Before | After |
| --- | --- |
| `China mainland` + `Chinese mainland` | `Chinese Mainland` |
| `Hong Kong` + `Hong kong sar china` | `Hong Kong SAR China` |
| `South Korea` + `South korea` | `South Korea` |
| `Czech republic` + `Czechia` | `Czechia` |

This trimmed the distinct-country count from 57 to 53.

## Image URL backfill

The image scraper is a separate `images-batch` subcommand (not part of
the main `scrape` pass), so the 2026 refresh left `image_url` empty on
every newly-scraped restaurant. 2,438 in-guide restaurants were
missing images by the end of the main pass, plus 1,296 delisted
restaurants that had never been image-scraped.

A single `./mym images-batch` run (no `-retry-failed`) was executed
after the green-star fix was merged. It processes each restaurant
serially, sleeps 2s between requests, and uses a standalone
`net/http` client (no colly cache). Wall time ≈ 2h 15m for 3,734
URLs.

Results:

| Bucket | Before | After | Δ |
| --- | ---: | ---: | ---: |
| In-guide with image | 18,103 | 20,541 | **+2,438** |
| In-guide missing image | 2,484 | 46 | **−2,438** |
| Delisted with image | 119 | 174 | +55 |
| Delisted 404s | 1,515 | 2,756 | +1,241 |

In-guide image coverage is now **99.78%** (20,541 / 20,587); the 46
remaining failures are pre-existing carry-overs, likely flaky pages.
The +1,241 new "failed" entries on delisted restaurants are 404s —
Michelin removed those pages entirely.

Minor known issue: GORM's `OnConflict` clause in
`storage.SaveRestaurant` uses the loaded-into-struct value of
`updated_at` rather than `CURRENT_TIMESTAMP`, so image-batch writes
don't bump the row's timestamp. Image URLs themselves land correctly;
just the audit timestamp doesn't reflect the update.

## Housekeeping

Additional cleanup folded into this branch during 0.2 packaging:

- Six dead-code siblings of `zipcode_location_columns.go` in
  `database builder/location_tool/` were deleted — they were
  pre-canonical iterations (`add_location_columns.go`,
  `complete_location_columns.go`, `conservative_…`, `final_accurate_…`,
  `final_…`, `improve_…`), all with `package main`, causing
  duplicate-declaration errors on `go build ./...`.
- `pkg/tools/add_location_columns.go` and
  `pkg/tools/compare_csv_database.go` were moved into their own
  subdirectories so each can stand as its own `package main`.
- `pkg/main.go`'s dead `db.UpdateDatabase` call (hardcoded dev path,
  ran on every Alfred invocation, always returned "no update
  available") was removed along with `NoUpdateAvailableError`,
  `IsNoUpdateAvailable`, and duplicate `extractZipFile` / `copyFile`
  in `pkg/db/database.go`.
- `.gitignore` now excludes `pkg/alfred-michelin`,
  `database builder/michelin-my-maps-3.2.1/mym`,
  `database builder/michelin-my-maps-3.2.1/cache/`, and SQLite WAL/SHM
  files.
- Zurich/Elmira (404 on rescrape, Michelin delisted) was flipped to
  `in_guide=0`.
- Six intermediate smoketest/rescrape DB backups were deleted from
  `data/`. The two milestone backups retained are
  `michelin.db.pre-refresh-backup` (pre-2026 scrape) and
  `michelin.db.pre-location-tool-backup` (post-scrape, pre-location-tool).

## Empty-price recovery pass

During the main run, 57 award rows were rejected by the award validator
because Michelin's page omitted the price range — the restaurant row saved
fine, but the award insert failed the `Price != ""` check. That would have
meant e.g. a newly promoted 3-star restaurant missing its 2026 award entry
entirely if Michelin hadn't listed a price range yet.

Resolution:

1. Relaxed the validator in `internal/models/award.go` — empty price is
   now treated as "unknown" and accepted.
2. Extracted the 57 URLs from the scrape log, built a CSV seeded from the
   restaurant rows already in the DB (name / location / address were
   all populated from the main pass), and ran `./mym scrape -csv` against
   just those URLs.
3. Targeted pass completed in 0.6s, 57 upserts, 0 failures. All 57 award
   rows are now in the DB with `price=''`.

UI / display-side callers should treat an empty `price` string as
"unknown" rather than assuming the field is populated.

## Differential scraper (`scrape-diff`)

The follow-up flagged in the initial report ("no incremental refresh")
was implemented in this same branch before the 2026 run was archived.
Usage:

```
./mym scrape-diff -year 2027
```

What it does, per listing URL seen during a single pass over the five
distinction listings:

| DB state | Action | Request cost |
| --- | --- | --- |
| URL not in DB | Fetch detail page, insert restaurant + award | 1 detail req |
| URL in DB, distinction differs from listing | Fetch detail page, upsert award | 1 detail req |
| URL in DB, latest award already ≥ target year | No-op (URL marked `seen` for sweep) | 0 |
| URL in DB, distinction matches, no row for target year | Clone price/greenStar/location forward into new award row | 0 |
| URL in DB but not surfaced in any listing | Sweep marks `in_guide=false` | 0 |

Detail fetches are reserved for actually-changed rows. In the smoke test
against the 2026 DB with `-year 2027` (synthetic, just to exercise the
branching), only 5 detail pages were fetched across ~21k listing events:

```
already_current=0 award_changed=4 dropped=1508 new=1 unchanged=19076
```

The 19,076 listing-only clones correctly took their price and greenStar
from the 2026 award row — that's the design goal, so a query like
"2027 distinction distribution" stays answerable without a full rescrape.

Key implementation files (all on `dev-scraper-resilience`):

- `internal/scraper/diff.go` — new file. `RunDiff`, `setupDiffMainHandlers`,
  `writeListingOnlyAward`, `sweepDroppedRestaurants`, `buildDistinctionResolver`.
  Uses `sync.Map.LoadOrStore` to dedupe URLs across pagination /
  cross-listing revisits so counters match actual DB writes.
- `internal/scraper/extract.go` — honors `diff_target_year` context key so
  detail-fetched rows save under the `-year` flag rather than whatever
  Michelin's JSON-LD reports.
- `internal/storage/sqlite.go` — `LatestAwardInfo`, `GetLatestAwardsByURL`
  (single correlated query returning `map[url]LatestAwardInfo`),
  `SetInGuide` (column-only update that bypasses the restaurant validator).
- `cmd/mym/mym.go` — `scrape-diff` subcommand with `-year` and `-log` flags.

Caveats:

- **Type-asserts to `*storage.SQLiteRepository`** — diff mode only works
  against the real SQLite repo, not the in-memory mock used by tests.
- **Listing-only clone inherits price from prior year.** If Michelin
  updates the price range for a restaurant but keeps its distinction,
  the diff pass won't catch it. Acceptable trade-off given price changes
  are rare and a full rescrape every 2–3 years will reconcile.
- **Distinction is derived from the starter URL path segment** via
  `buildDistinctionResolver`. If Michelin renames one of the five
  starter URLs, the resolver will return `""` and diff falls back to
  fetching the detail page for that URL.

## Known issues / follow-ups

- **End-user update path bug (fixed in this branch).** `pkg/main.go`
  `preserveUserData` previously copied `user_favorites.restaurant_id`
  verbatim during workflow updates. Because restaurant IDs are
  autoincrement and not stable across scrapes, this would silently
  misalign favorites/visits for any user with data. Replaced with a
  delegation to `db.PreserveUserDataDuringUpdate`, which maps by URL.
- **`UpdateDatabase` dev-only path.** `pkg/db/database.go:1031`
  hardcodes `/Users/giovanni/gDrive/GitHub repos/alfred-michelin/source`.
  Harmless for end users (returns `NoUpdateAvailableError`) but worth
  cleaning up.
