# Michelin Guide ✨️
An Alfred workflow to search, favorite, and track your visits to **23,000+** Michelin guide restaurants around the world, refreshed with the 2026 guide.\
Feeling fancy? 🎩 Find your next Michelin-starred restaurant with Alfred. 🌟

<a href="https://github.com/giovannicoppola/alfred-michelin/releases/latest/">
<img alt="Downloads"
src="https://img.shields.io/github/downloads/giovannicoppola/alfred-michelin/total?color=purple&label=Downloads"><br/>
</a>
<a href="https://alfred.app/workflows/giovannicoppola/michelin-guide/">
<img alt="Gallery Downloads"
src="https://img.shields.io/badge/dynamic/json?url=https%3A%2F%2Fraw.githubusercontent.com%2Fgiovannicoppola%2Falfred-gallery-downloads%2Fmain%2Fdownloads.json&query=%24.michelin-guide%5B0%5D.display&label=Gallery%20Downloads&color=blue&logo=alfred"><br/>
</a>

![](source/screenshot.png)

## Features

### 🔍 Smart Search
- **Comprehensive Search**: Search through 23,000+ Michelin guide restaurants by name, location, cuisine, or distinction (stars)
- **Case-Sensitive Search**: When you search with all-caps or partial-caps terms (e.g., "USA", "Italy", "Hill"), the search becomes case-sensitive to avoid false matches
- **Country & State Filters**: Narrow to a specific country or US state with `country:japan` / `state:ca` — combine with any other filter
- **Multi-term Search**: Combine multiple search terms (e.g., "USA 3s" finds 3-star restaurants in the USA)
- **Real-time Results**: Instant search results with restaurant details displayed in Alfred

### 📍 Restaurant Information
- **Complete Details**: View restaurant name, address, location, price range, cuisine type, and Michelin distinctions
- **Award History**: Access complete award history for each restaurant (SHIFT modifier)
- **Current Status**: See if restaurants are currently in the guide or have been removed (marked with 📜)
- **Visual Indicators**: Stars displayed as emojis (⭐️⭐️⭐️ for 3-star, etc.) with green star indicators (🍀)

### ❤️ Favorites Management
- **Save Favorites**: Add restaurants to your personal favorites list (CTRL modifier)
- **Quick Access**: View all your favorite restaurants with `!mf` command
- **Search Favorites**: Search within your favorites using `!mf [query]`
- **Visual Indicators**: Heart emoji (❤️) shows favorite status

### ✅ Visit Tracking
- **Track Visits**: Mark restaurants as visited with date and personal notes (ALT modifier)
- **Visit History**: View all visited restaurants with `!mv` command
- **Search Visits**: Search within your visited restaurants using `!mv [query]`
- **Visual Indicators**: Checkmark emoji (✅) shows visited status

### 🌐 External Integration
- **Website Access**: Open restaurant websites directly from Alfred
- **Michelin Guide**: View restaurants on the official Michelin Guide website
- **Maps Integration**: Open restaurant locations in Google Maps or Apple Maps
- **Image Display**: View restaurant images with descriptions (SHIFT modifier)


## Usage

### Primary Commands (default keywords, set your own or hotkeys)
- `!mm [query]` - Search for Michelin restaurants by name, location, cuisine, or distinction
- `!mf` - View all your favorite restaurants
- `!mf [query]` - Search within your favorite restaurants
- `!mv` - View all restaurants you've visited
- `!mv [query]` - Search within your visited restaurants

## Search Examples

### Basic Search
- `!mm "Providence"` - Find specific restaurant
- `!mm "New York"` - Find restaurants in New York
- `!mm "Italian"` - Find Italian cuisine restaurants
- `!mm "3s"` - Find all 3-star restaurants

### Case-Sensitive Search
- `!mm "USA"` - Case-sensitive search for USA (won't match "Da Vittorio" in Italy)
- `!mm "Italy"` - Case-sensitive search for Italy
- `!mm "Hill"` - Case-sensitive search for Hill (won't match "hill" in other words)

### Combined Search
- `!mm "USA 3s"` - Find 3-star restaurants in the USA
- `!mm "Italy 2 star"` - Find 2-star restaurants in Italy
- `!mm "France Michelin"` - Find Michelin restaurants in France

### Country and State Filters
- `!mm "country:japan"` - All restaurants in Japan
- `!mm "country:japan 3s"` - 3-star restaurants in Japan
- `!mm "country:united"` - Substring match: hits both United Kingdom and United States
- `!mm "state:ca"` - All restaurants in California (2-letter state code, exact match)
- `!mm "state:ny bg"` - Bib Gourmand restaurants in New York
- `!mm "state:ca sushi"` - Sushi restaurants in California
- `!mm "country:japan --current"` - If you run with `INCLUDE_FORMER=1` by default, append `--current` to drop delisted (📜) restaurants for one query

Filter tokens:

| Token | Meaning |
| --- | --- |
| `1s` / `2s` / `3s` | 1-/2-/3-star restaurants |
| `bg` | Bib Gourmand |
| `sr` | Selected Restaurants |
| `gs` | Green Star recipients |
| `country:<name>` | Country (substring match, case-insensitive) |
| `state:<xx>` | US state (2-letter code, exact match) |
| `--current` | Restrict to restaurants currently in the guide, overriding `INCLUDE_FORMER=1` for this one query |

Notes:

- Filter tokens **cannot contain spaces** — for multi-word countries like `United Kingdom`, `Hong Kong SAR China`, or `South Korea` use a distinctive substring (`country:united`, `country:hong`, `country:korea`).
- `state:` is US-only. For non-US regions use `country:` instead.
- To see the full list of countries and US states currently in the database, run `./michelin report` and check the "Top 20 countries" and "Top 20 US states" sections.

## Once a restaurant is identified: 

- `CTRL`: **❤️Favorite**: Toggle restaurant favorite status
- `ALT`: **✅️Visited**: Toggle restaurant visited status
- `CMD`: **🏆️Awards**: View award history for restaurant (CMD+ALT = back)
- `SHIFT`: **ℹ️More details**

## Installation

1. Download the latest release from the [releases page](https://github.com/giovannicoppola/alfred-michelin/releases/latest)
2. Double-click the `.alfredworkflow` file to install it in Alfred
3. The workflow will automatically set up the database on first use

## Data Source

This workflow uses data from the Michelin Guide [dataset](https://www.kaggle.com/datasets/ngshiheng/michelin-guide-restaurants-2021) and [scripts](https://github.com/ngshiheng/michelin-my-maps/tree/main) generated by [Jerry Ng](https://github.com/ngshiheng) 

## What's new in version 0.2

### 🗓️ Refreshed with the 2026 Michelin guide

How the bundled database changed between version 0.1 (July 2025 snapshot) and 0.2 (April 2026 snapshot):

| Metric | v0.1 | v0.2 | Δ |
| --- | ---: | ---: | ---: |
| Total restaurants | 21,092 | 23,517 | **+2,425** |
| Currently in guide | 18,131 | 20,587 | **+2,456** |
| Award rows (all years) | 68,813 | 76,404 | **+7,591** |
| Years covered | 2018 – 2025 | 2018 – 2026 | +2026 |
| Distinct countries | 57 | 53\* | |

\* v0.2 merged four casing duplicates (e.g. `Chinese mainland` / `China mainland` → `Chinese Mainland`).

Current distinctions (most recent award per restaurant, in-guide only):

| Distinction | v0.1 | v0.2 | Δ |
| --- | ---: | ---: | ---: |
| 3 Stars ⭐⭐⭐ | 157 | 160 | +3 |
| 2 Stars ⭐⭐ | 507 | 559 | +52 |
| 1 Star ⭐ | 3,084 | 3,324 | +240 |
| Bib Gourmand | 3,354 | 3,840 | +486 |
| Selected Restaurants | 11,029 | 12,704 | +1,675 |
| Green Star 🌱 (any year) | 650 | 588 | −62 |

The green-star drop is a real signal, not a bug: many restaurants that previously held a green star were either delisted or no longer flagged for one in the 2026 guide. 200 green stars are on 2026 awards specifically; the rest are on prior years for restaurants still in the guide.

Retired restaurants are retained in the DB and shown with the 📜 marker so award history stays queryable.

### 🌍 Country and US-state search
- Every restaurant now carries a normalized `country` (e.g. `France`, `USA`, `Japan`) and, for US restaurants, a 2-letter `us_state` code.
- Coverage: 100% of restaurants have a country; 100% of US restaurants have a state.
- New filter syntax in the main search:
  - `country:japan`, `country:united` (substring match)
  - `state:ca`, `state:ny` (exact 2-letter match)
  - Combine freely with existing filters like `1s`, `bg`, `gs`.

### 🌱 Green Stars
- Sustainability Green Stars are fully captured for the 2026 guide: **200 restaurants newly recovered for 2026** and refreshed data for 2025 — **588 distinct recipients in total**, including first-time 2026 additions.
- You can filter for them with the `gs` token (e.g. `!mm "gs"` or `!mm "gs country:france"`).

### 📊 Built-in markdown report
- Run `./michelin report` to print a full database snapshot to stdout, or `./michelin report path/to/out.md` to write it to a file.
- Includes: totals, in-guide vs. removed, green-star recipients (total and per-year), awards by year, top 20 countries with per-distinction breakdown, top 20 US states, top 20 cuisines, and personal favorites/visits counts.

### 🔐 Safer updates
- Per-update timestamped backups (`michelin_backup_<UTC>.db`) instead of a single rolling file, so a mid-update failure doesn't wipe the previous safety net. Only the three most recent backups are kept.
- User favorites and visits are re-mapped by **URL** (stable) instead of autoincrement restaurant ID (not stable across data refreshes), so favorites/visits stay pointed at the right restaurants after every update.
- Removed dead code path that was running a failed update check on every single launch.

## Roadmap

- Direct database updates via Kaggle Hub
- map visualization?

## License

MIT

## Changelog

- **2026-06-29 Version 0.2** – 2026 Michelin data refresh (April 2026 snapshot), country / US-state search filters, `--current` filter token, markdown report generator, Green Stars fully captured (200 restaurants recovered for 2026, 588 in-guide total), safer update path.
- **2025-07-13 Version 0.1** – First release.

## Acknowledgments

- Michelin Guide for restaurant data
- Jerry Ng for the [Michelin My Maps](https://github.com/ngshiheng/michelin-my-maps/tree/main) scripts
- Cursor AI for help with coding and writing this README
- Alfred team for the amazing workflow platform
- Open source community for inspiration and tools
 
