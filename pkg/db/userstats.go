package db

import (
	"database/sql"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Personal stats: the restaurants you have visited or favorited, not the whole
// guide. GatherUserStats reads them; UserStatsMarkdown renders the summary for
// Alfred's Text View and UserStatsHTML the fuller report the Text View's ↩
// opens. Distinctions are each restaurant's most recent award, as everywhere
// else in the workflow.

// UserPlace is one visited and/or favorited restaurant.
type UserPlace struct {
	ID          int64
	Name        string
	Location    string
	Country     string
	State       string
	Cuisine     string
	URL         string
	Website     string
	Distinction string
	Price       string
	InGuide     bool
	GreenStar   bool
	Visited     bool
	VisitedDate string
	Favorite    bool
}

// UserStats holds everything both renderings need.
type UserStats struct {
	Visited   []UserPlace // most recent first
	Favorites []UserPlace
	// In-guide restaurants per current distinction, for "10 of 160 three-star".
	GuideByDistinction map[string]int
	// In-guide starred restaurants per location, for the city coverage table.
	StarredByLocation map[string]int
	Generated         time.Time
}

var distinctionOrder = []string{"3 Stars", "2 Stars", "1 Star", "Bib Gourmand", "Selected Restaurants"}

// latestAwardSQL picks each restaurant's most recent award row.
const latestAwardSQL = `
	SELECT ra.restaurant_id, ra.distinction, ra.price
	FROM restaurant_awards ra
	JOIN (SELECT restaurant_id, MAX(year) AS max_year FROM restaurant_awards GROUP BY restaurant_id) latest
	  ON latest.restaurant_id = ra.restaurant_id AND latest.max_year = ra.year`

// GatherUserStats reads the visited and favorite restaurants plus the guide
// totals they are measured against.
func GatherUserStats(db *sql.DB) (*UserStats, error) {
	s := &UserStats{
		GuideByDistinction: map[string]int{},
		StarredByLocation:  map[string]int{},
		Generated:          time.Now(),
	}
	rows, err := db.Query(`
		WITH latest AS (` + latestAwardSQL + `)
		SELECT r.id, COALESCE(r.name, ''), COALESCE(r.location, ''), COALESCE(r.country, ''),
		       COALESCE(r.us_state, ''), COALESCE(r.cuisine, ''), COALESCE(r.url, ''),
		       COALESCE(r.website_url, ''), COALESCE(l.distinction, ''), COALESCE(l.price, ''),
		       COALESCE(r.in_guide, 0),
		       -- any year, as in the database report: the 2026 pages dropped the attribute
		       EXISTS(SELECT 1 FROM restaurant_awards g WHERE g.restaurant_id = r.id AND g.green_star = 1),
		       uv.restaurant_id IS NOT NULL, COALESCE(uv.visited_date, ''),
		       uf.restaurant_id IS NOT NULL
		FROM restaurants r
		LEFT JOIN user_visits uv ON uv.restaurant_id = r.id
		LEFT JOIN user_favorites uf ON uf.restaurant_id = r.id
		LEFT JOIN latest l ON l.restaurant_id = r.id
		WHERE uv.restaurant_id IS NOT NULL OR uf.restaurant_id IS NOT NULL
		ORDER BY uv.visited_date DESC, r.name
	`)
	if err != nil {
		return nil, fmt.Errorf("user places: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p UserPlace
		var inGuide, green, visited, favorite int
		if err := rows.Scan(&p.ID, &p.Name, &p.Location, &p.Country, &p.State, &p.Cuisine, &p.URL,
			&p.Website, &p.Distinction, &p.Price, &inGuide, &green, &visited, &p.VisitedDate, &favorite); err != nil {
			return nil, err
		}
		p.InGuide, p.GreenStar, p.Visited, p.Favorite = inGuide == 1, green == 1, visited == 1, favorite == 1
		if p.Visited {
			s.Visited = append(s.Visited, p)
		}
		if p.Favorite {
			s.Favorites = append(s.Favorites, p)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	totals, err := db.Query(`
		WITH latest AS (` + latestAwardSQL + `)
		SELECT l.distinction, COALESCE(r.location, ''), COUNT(*)
		FROM restaurants r JOIN latest l ON l.restaurant_id = r.id
		WHERE r.in_guide = 1
		GROUP BY l.distinction, r.location
	`)
	if err != nil {
		return nil, fmt.Errorf("guide totals: %w", err)
	}
	defer totals.Close()
	for totals.Next() {
		var d, loc string
		var n int
		if err := totals.Scan(&d, &loc, &n); err != nil {
			return nil, err
		}
		s.GuideByDistinction[d] += n
		if starCount(d) > 0 {
			s.StarredByLocation[loc] += n
		}
	}
	return s, totals.Err()
}

func starCount(distinction string) int {
	switch distinction {
	case "3 Stars":
		return 3
	case "2 Stars":
		return 2
	case "1 Star":
		return 1
	}
	return 0
}

// priceLevel turns "$$$", "€€", "฿฿฿฿" into 1–4, whatever the currency.
func priceLevel(price string) int {
	return utf8.RuneCountInString(strings.TrimSpace(price))
}

type countRow struct {
	Key string
	N   int
	Sub int // a second count, e.g. starred among them
}

// countBy tallies places by key, most frequent first; ties alphabetically.
func countBy(places []UserPlace, key func(UserPlace) string, sub func(UserPlace) bool) []countRow {
	m := map[string]*countRow{}
	for _, p := range places {
		k := key(p)
		if k == "" {
			k = "(unknown)"
		}
		r, ok := m[k]
		if !ok {
			r = &countRow{Key: k}
			m[k] = r
		}
		r.N++
		if sub != nil && sub(p) {
			r.Sub++
		}
	}
	out := make([]countRow, 0, len(m))
	for _, r := range m {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func countryLabel(p UserPlace) string {
	if p.Country == "USA" && p.State != "" {
		return "USA · " + p.State
	}
	return p.Country
}

func isStarred(p UserPlace) bool { return starCount(p.Distinction) > 0 }

// coverage reads "30 of 62": starred restaurants visited out of those in the
// guide at that location; a dash where the guide has none there.
func coverage(visited, inGuide int) string {
	if inGuide == 0 {
		return "–"
	}
	return fmt.Sprintf("%d of %d", visited, inGuide)
}

type distinctionRow struct {
	Name     string
	Visited  int
	Favorite int
	InGuide  int
}

func (s *UserStats) distinctions() []distinctionRow {
	var out []distinctionRow
	for _, d := range distinctionOrder {
		r := distinctionRow{Name: d, InGuide: s.GuideByDistinction[d]}
		for _, p := range s.Visited {
			if p.Distinction == d {
				r.Visited++
			}
		}
		for _, p := range s.Favorites {
			if p.Distinction == d {
				r.Favorite++
			}
		}
		out = append(out, r)
	}
	return out
}

func (s *UserStats) starsCollected() int {
	n := 0
	for _, p := range s.Visited {
		n += starCount(p.Distinction)
	}
	return n
}

func (s *UserStats) toTry() []UserPlace {
	var out []UserPlace
	for _, p := range s.Favorites {
		if !p.Visited {
			out = append(out, p)
		}
	}
	sortByDistinction(out)
	return out
}

func sortByDistinction(ps []UserPlace) {
	rank := map[string]int{}
	for i, d := range distinctionOrder {
		rank[d] = i
	}
	sort.SliceStable(ps, func(i, j int) bool {
		ri, ok := rank[ps[i].Distinction]
		if !ok {
			ri = len(rank)
		}
		rj, ok := rank[ps[j].Distinction]
		if !ok {
			rj = len(rank)
		}
		if ri != rj {
			return ri < rj
		}
		return ps[i].Name < ps[j].Name
	})
}

func (s *UserStats) months() map[[2]int]int {
	m := map[[2]int]int{}
	for _, p := range s.Visited {
		t, err := time.Parse("2006-01-02", firstN(p.VisitedDate, 10))
		if err == nil {
			m[[2]int{t.Year(), int(t.Month())}]++
		}
	}
	return m
}

func (s *UserStats) countWith(pred func(UserPlace) bool) int {
	n := 0
	for _, p := range s.Visited {
		if pred(p) {
			n++
		}
	}
	return n
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ------------------------------------------------------------------ shading

// Shades for the month grid, lightest to heaviest. A code block is the only
// place Alfred renders a fixed-width grid, so the "colours" are characters.
var calChars = []string{"·", "░", "▒", "▓", "█"}
var monthAbbr = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// thresholds gives quartile cut-offs for the four non-empty shades, as in
// alfred-readwise: fixed cut-offs would be all pale or all dark.
func thresholds(counts map[[2]int]int) [3]int {
	var nz []int
	for _, c := range counts {
		if c > 0 {
			nz = append(nz, c)
		}
	}
	if len(nz) == 0 {
		return [3]int{1, 2, 3}
	}
	sort.Ints(nz)
	var out [3]int
	for i, p := range []float64{0.25, 0.5, 0.75} {
		idx := int(float64(len(nz)) * p)
		if idx > len(nz)-1 {
			idx = len(nz) - 1
		}
		v := nz[idx]
		floor := 1
		if i > 0 {
			floor = out[i-1] + 1
		}
		if v < floor {
			v = floor // strictly increasing, or two shades would mean the same thing
		}
		out[i] = v
	}
	return out
}

func level(n int, th [3]int) int {
	if n <= 0 {
		return 0
	}
	for i, t := range th {
		if n <= t {
			return i + 1
		}
	}
	return 4
}

func bands(th [3]int) []string {
	band := func(lo, hi int) string {
		if lo == hi {
			return fmt.Sprint(lo)
		}
		return fmt.Sprintf("%d–%d", lo, hi)
	}
	return []string{"none", band(1, th[0]), band(th[0]+1, th[1]), band(th[1]+1, th[2]), fmt.Sprintf(">%d", th[2])}
}

func yearRange(m map[[2]int]int) (int, int) {
	lo, hi := 0, 0
	for k := range m {
		if lo == 0 || k[0] < lo {
			lo = k[0]
		}
		if k[0] > hi {
			hi = k[0]
		}
	}
	return lo, hi
}

// ------------------------------------------------------------------ Markdown

// mdTable lays out rows as fixed-width columns inside a code block: Alfred's
// Markdown has no tables. align is one of '<' / '>' per column.
func mdTable(headers []string, rows [][]string, align string) []string {
	if len(rows) == 0 {
		return nil
	}
	w := make([]int, len(headers))
	for i, h := range headers {
		w[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if n := utf8.RuneCountInString(c); n > w[i] {
				w[i] = n
			}
		}
	}
	line := func(cells []string) string {
		parts := make([]string, len(cells))
		for i, c := range cells {
			pad := strings.Repeat(" ", w[i]-utf8.RuneCountInString(c))
			if align[i] == '>' {
				parts[i] = pad + c
			} else {
				parts[i] = c + pad
			}
		}
		return strings.TrimRight(strings.Join(parts, "  "), " ")
	}
	dashes := make([]string, len(headers))
	for i := range headers {
		dashes[i] = strings.Repeat("-", w[i])
	}
	out := []string{"```", line(headers), line(dashes)}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return append(out, "```", "")
}

func mdBar(v, max, width int) string {
	if max <= 0 || v <= 0 {
		return ""
	}
	n := (v*width + max/2) / max
	if n < 1 {
		n = 1
	}
	return strings.Repeat("▇", n)
}

func short(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func pctOf(n, total int) string {
	if total == 0 {
		return "-"
	}
	p := 100 * float64(n) / float64(total)
	if p > 0 && p < 0.1 {
		return "<0.1%"
	}
	if p > 0 && p < 10 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

func mdMonthGrid(m map[[2]int]int) []string {
	lo, hi := yearRange(m)
	th := thresholds(m)
	head := "      "
	for _, a := range monthAbbr {
		head += fmt.Sprintf("%-4s", a)
	}
	out := []string{"```", head + "  n"}
	for y := lo; y <= hi; y++ {
		line := fmt.Sprintf("%-6d", y)
		total := 0
		for mo := 1; mo <= 12; mo++ {
			n := m[[2]int{y, mo}]
			total += n
			line += " " + calChars[level(n, th)] + "  "
		}
		out = append(out, line+fmt.Sprintf("%3d", total))
	}
	legend := make([]string, 5)
	for i, b := range bands(th) {
		legend[i] = calChars[i] + " " + b
	}
	return append(out, "", "      "+strings.Join(legend, "  ")+"   (visits per month)", "```", "")
}

// UserStatsMarkdown renders the Text View summary.
func UserStatsMarkdown(s *UserStats) string {
	V, F := s.Visited, s.Favorites
	toTry := s.toTry()
	md := []string{"# 🍽️ Your Michelin restaurants", ""}
	md = append(md, fmt.Sprintf("**%s** visited · **%s** favorites (%s visited, %s still to try) · "+
		"**%s** stars collected.", fmtInt(len(V)), fmtInt(len(F)), fmtInt(len(F)-len(toTry)),
		fmtInt(len(toTry)), fmtInt(s.starsCollected())), "")

	md = append(md, "## By distinction", "", "Each restaurant's current distinction, measured against the whole guide.", "")
	var rows [][]string
	for _, d := range s.distinctions() {
		rows = append(rows, []string{d.Name, fmtInt(d.Visited), fmtInt(d.InGuide), pctOf(d.Visited, d.InGuide), fmtInt(d.Favorite)})
	}
	md = append(md, mdTable([]string{"distinction", "visited", "in guide", "share", "favorites"}, rows, "<>>>>")...)
	if gs := s.countWith(func(p UserPlace) bool { return p.GreenStar }); gs > 0 {
		md = append(md, fmt.Sprintf("Green Stars: %s of your visits.", fmtInt(gs)), "")
	}
	if gone := s.countWith(func(p UserPlace) bool { return !p.InGuide }); gone > 0 {
		md = append(md, fmt.Sprintf("No longer in the guide: %s.", fmtInt(gone)), "")
	}

	md = append(md, "## Where", "")
	byCountry := countBy(V, countryLabel, isStarred)
	maxN := 0
	if len(byCountry) > 0 {
		maxN = byCountry[0].N
	}
	rows = nil
	for _, r := range byCountry {
		rows = append(rows, []string{r.Key, fmtInt(r.N), mdBar(r.N, maxN, 16), fmtInt(r.Sub)})
	}
	md = append(md, mdTable([]string{"country", "visited", "", "starred"}, rows, "<><>")...)

	byCity := countBy(V, func(p UserPlace) string { return p.Location }, isStarred)
	rows = nil
	for i, r := range byCity {
		if i == 8 {
			break
		}
		rows = append(rows, []string{short(r.Key, 30), fmtInt(r.N), coverage(r.Sub, s.StarredByLocation[r.Key])})
	}
	md = append(md, "Top cities, with the starred restaurants you have been to there:", "")
	md = append(md, mdTable([]string{"city", "visited", "starred"}, rows, "<><")...)

	md = append(md, "## Cuisines and prices", "")
	rows = nil
	for i, r := range countBy(V, func(p UserPlace) string { return p.Cuisine }, nil) {
		if i == 10 {
			break
		}
		rows = append(rows, []string{short(r.Key, 32), fmtInt(r.N)})
	}
	md = append(md, mdTable([]string{"cuisine", "visited"}, rows, "<>")...)
	rows = nil
	for lvl := 4; lvl >= 1; lvl-- {
		n := s.countWith(func(p UserPlace) bool { return priceLevel(p.Price) == lvl })
		rows = append(rows, []string{strings.Repeat("$", lvl), fmtInt(n), mdBar(n, len(V), 16)})
	}
	md = append(md, "Price level (any currency):", "")
	md = append(md, mdTable([]string{"price", "visited", ""}, rows, "<><")...)

	if m := s.months(); len(m) > 0 {
		md = append(md, "## By month", "", "The date a restaurant was marked visited, which may be later than the meal.", "")
		md = append(md, mdMonthGrid(m)...)
	}

	md = append(md, "## Most recent visits", "")
	rows = nil
	for i, p := range V {
		if i == 8 {
			break
		}
		rows = append(rows, []string{short(p.Name, 30), short(p.Location, 24), short(p.Distinction, 20), firstN(p.VisitedDate, 10)})
	}
	md = append(md, mdTable([]string{"restaurant", "city", "distinction", "visited"}, rows, "<<<<")...)

	md = append(md, "## Favorites still to try", "")
	if len(toTry) == 0 {
		md = append(md, "None: you have been to every favorite.", "")
	} else {
		rows = nil
		for _, p := range toTry {
			rows = append(rows, []string{short(p.Name, 30), short(p.Location, 26), short(p.Distinction, 20)})
		}
		md = append(md, mdTable([]string{"restaurant", "city", "distinction"}, rows, "<<<")...)
	}
	md = append(md, "*↩ opens the full HTML report · ⌘↩ copies this summary*")
	return strings.Join(md, "\n")
}

// ------------------------------------------------------------------ HTML

const userStatsCSS = `
:root { color-scheme: light dark;
  --bg:#fbf9f8; --panel:#ffffff; --ink:#221c1b; --muted:#6f6664; --line:#ebe4e2;
  --accent:#ba0c2f; --l0:#f2eeed; --l1:#f5cdd3; --l2:#e8909e; --l3:#d24a63; --l4:#ba0c2f; }
@media (prefers-color-scheme: dark) { :root {
  --bg:#141111; --panel:#1d1918; --ink:#ede7e5; --muted:#a69c99; --line:#322b29;
  --accent:#ff5c77; --l0:#262120; --l1:#4a2128; --l2:#7d2b3a; --l3:#c23f58; --l4:#ff6f88; } }
* { box-sizing:border-box; }
body { margin:0; background:var(--bg); color:var(--ink);
  font:15px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
.wrap { max-width:980px; margin:0 auto; padding:40px 24px 96px; }
h1 { font-size:30px; margin:0 0 6px; letter-spacing:-0.02em; }
h2 { font-size:20px; margin:44px 0 14px; letter-spacing:-0.01em; }
h3 { font-size:13px; margin:24px 0 10px; color:var(--muted); font-weight:600;
  text-transform:uppercase; letter-spacing:0.06em; }
p.lede { color:var(--muted); margin:0 0 20px; }
a { color:var(--accent); text-decoration:none; } a:hover { text-decoration:underline; }
.tiles { display:grid; grid-template-columns:repeat(auto-fit, minmax(130px, 1fr)); gap:12px; }
.tile { background:var(--panel); border:1px solid var(--line); border-radius:10px; padding:14px 16px; }
.tile .n { font-size:24px; font-weight:650; letter-spacing:-0.02em; font-variant-numeric:tabular-nums; }
.tile .k { font-size:12px; color:var(--muted); text-transform:uppercase; letter-spacing:0.06em; }
.tablewrap { overflow-x:auto; background:var(--panel); border:1px solid var(--line); border-radius:10px; }
table { width:100%; border-collapse:collapse; font-size:14px; }
th, td { text-align:left; padding:7px 10px; border-bottom:1px solid var(--line); }
tr:last-child td { border-bottom:0; }
th { font-size:11px; text-transform:uppercase; letter-spacing:0.07em; color:var(--muted); font-weight:600; }
td.n, th.n { text-align:right; font-variant-numeric:tabular-nums; white-space:nowrap; }
td.bar, th.bar { width:22%; min-width:80px; }
td.m { color:var(--muted); }
.meter { display:block; height:7px; border-radius:4px; background:var(--accent); min-width:2px; }
.grid td { padding:3px; border:0; text-align:center; font-size:11px; color:var(--muted); }
.grid td.y { text-align:left; padding-right:10px; font-variant-numeric:tabular-nums; }
.grid td.t { text-align:right; padding-left:10px; font-variant-numeric:tabular-nums; }
.cell { width:100%; min-width:18px; height:18px; border-radius:4px; }
.legend { display:flex; align-items:center; gap:4px; color:var(--muted); font-size:12px; margin:8px 0 0; }
.legend i { display:inline-block; width:12px; height:12px; border-radius:3px; }
.two { display:grid; grid-template-columns:1fr 1fr; gap:16px; }
input.filter { width:100%; padding:9px 12px; margin:0 0 12px; font:inherit; color:var(--ink);
  background:var(--panel); border:1px solid var(--line); border-radius:8px; }
@media (max-width:720px) { .two { grid-template-columns:1fr; } .wrap { padding:28px 16px 64px; } }
.collapsible:not(.open) .more { display:none; }
.more-toggle { margin:10px 0 0; padding:6px 12px; font:inherit; font-size:13px; color:var(--accent);
  background:var(--panel); border:1px solid var(--line); border-radius:8px; cursor:pointer; }
.more-toggle:hover { border-color:var(--accent); }
.modes { display:flex; align-items:center; gap:6px; margin:-4px 0 12px; font-size:13px; color:var(--muted); }
.modes span { margin-right:4px; }
.modes button, .pager button { font:inherit; font-size:13px; padding:5px 11px; color:var(--ink);
  background:var(--panel); border:1px solid var(--line); border-radius:8px; cursor:pointer; }
.modes button:hover, .pager button:hover:not(:disabled) { border-color:var(--accent); }
.modes button.on, .pager button.on { background:var(--accent); border-color:var(--accent); color:var(--bg); }
.pager { display:flex; flex-wrap:wrap; align-items:center; gap:6px; margin:12px 0 0; }
.pager .info { color:var(--muted); font-size:13px; margin-right:auto; font-variant-numeric:tabular-nums; }
.pager button:disabled { opacity:.4; cursor:default; }
.pager .gap { color:var(--muted); padding:0 2px; }
footer { margin-top:60px; color:var(--muted); font-size:12px; border-top:1px solid var(--line); padding-top:16px; }
`

const userStatsJS = `
<script>
// The full list (#all): filtered by the box above it, then shown 25 rows at a
// time, either as pages or as an infinite scroll. The choice is remembered.
(function () {
  var table = document.getElementById('all'), box = document.getElementById('filter');
  if (!table || !box) return;
  var SIZE = 25, page = 0, limit = SIZE, mode = 'pages';
  var rows = Array.prototype.slice.call(table.querySelectorAll('tr[data-s]')), matches = rows;
  try { mode = localStorage.getItem('listMode') === 'scroll' ? 'scroll' : 'pages'; } catch (e) {}

  var modes = document.createElement('div');
  modes.className = 'modes';
  modes.innerHTML = '<span>Show as</span><button type="button" data-m="pages">Pages</button>' +
    '<button type="button" data-m="scroll">Infinite scroll</button>';
  box.parentNode.insertBefore(modes, box.nextSibling);
  var wrap = table.parentNode;
  var pager = document.createElement('nav');
  pager.className = 'pager';
  wrap.parentNode.insertBefore(pager, wrap.nextSibling);

  function button(label, onClick, disabled, current) {
    var b = document.createElement('button');
    b.type = 'button';
    b.textContent = label;
    b.disabled = !!disabled;
    if (current) b.className = 'on';
    b.addEventListener('click', onClick);
    pager.appendChild(b);
  }
  function goTo(p) {
    return function () {
      page = p;
      render();
      var top = wrap.getBoundingClientRect().top;
      if (top < 0) window.scrollBy(0, top - 16);
    };
  }
  // first, last, and the pages either side of the current one; gaps as "…"
  function pageNumbers(n) {
    var out = [], last = -1;
    for (var i = 0; i < n; i++) {
      if (i === 0 || i === n - 1 || Math.abs(i - page) <= 1) {
        if (last >= 0 && i - last > 1) out.push(-1);
        out.push(i);
        last = i;
      }
    }
    return out;
  }
  function render() {
    var start = mode === 'pages' ? page * SIZE : 0;
    var end = Math.min(mode === 'pages' ? start + SIZE : limit, matches.length);
    rows.forEach(function (r) { r.style.display = 'none'; });
    matches.slice(start, end).forEach(function (r) { r.style.display = ''; });
    Array.prototype.forEach.call(modes.querySelectorAll('button'), function (b) {
      b.classList.toggle('on', b.dataset.m === mode);
    });
    pager.innerHTML = '';
    var info = document.createElement('span');
    info.className = 'info';
    info.textContent = matches.length
      ? (start + 1).toLocaleString() + '–' + end.toLocaleString() + ' of ' + matches.length.toLocaleString()
      : 'Nothing matches';
    pager.appendChild(info);
    if (mode === 'pages') {
      var pages = Math.ceil(matches.length / SIZE);
      if (pages > 1) {
        button('‹ Prev', goTo(page - 1), page === 0);
        pageNumbers(pages).forEach(function (p) {
          if (p < 0) {
            var gap = document.createElement('span');
            gap.className = 'gap';
            gap.textContent = '…';
            pager.appendChild(gap);
          } else {
            button(String(p + 1), goTo(p), false, p === page);
          }
        });
        button('Next ›', goTo(page + 1), page === pages - 1);
      }
    } else if (end < matches.length) {
      button('Load more', function () { limit += SIZE; render(); });
    }
  }
  // infinite scroll: top up while the bottom of the list is near the viewport
  function topUp() {
    if (mode !== 'scroll' || limit >= matches.length) return;
    if (pager.getBoundingClientRect().top < window.innerHeight + 400) {
      limit += SIZE;
      render();
      window.requestAnimationFrame(topUp);
    }
  }
  window.addEventListener('scroll', topUp, { passive: true });

  box.addEventListener('input', function () {
    var words = box.value.toLowerCase().split(' ').filter(Boolean);
    matches = rows.filter(function (r) {
      return words.every(function (w) { return r.dataset.s.indexOf(w) !== -1; });
    });
    page = 0;
    limit = SIZE;
    render();
  });
  modes.addEventListener('click', function (ev) {
    var m = ev.target.dataset ? ev.target.dataset.m : null;
    if (!m || m === mode) return;
    mode = m;
    page = 0;
    limit = SIZE;
    try { localStorage.setItem('listMode', mode); } catch (e) {}
    render();
    topUp();
  });
  render();
})();
// "Show N more" on the shorter lists above
Array.prototype.forEach.call(document.querySelectorAll('.more-toggle'), function (btn) {
  var n = btn.dataset.n;
  btn.addEventListener('click', function () {
    var open = btn.parentNode.classList.toggle('open');
    btn.textContent = open ? 'Show fewer' : 'Show ' + Number(n).toLocaleString() + ' more';
  });
});
</script>
`

var esc = html.EscapeString

func meter(v, max int) string {
	w := 0.0
	if max > 0 && v > 0 {
		w = 100 * float64(v) / float64(max)
		if w < 1.5 {
			w = 1.5
		}
	}
	return fmt.Sprintf(`<span class="meter" style="width:%.1f%%"></span>`, w)
}

// placeLink links the Michelin guide page, falling back to the restaurant's site.
func placeLink(p UserPlace) string {
	u := p.URL
	if u == "" {
		u = p.Website
	}
	if u == "" {
		return esc(p.Name)
	}
	return fmt.Sprintf(`<a href="%s">%s</a>`, esc(u), esc(p.Name))
}

// htmlTable: cells are already-escaped HTML; classes gives each column's class.
// Long lists show their first collapseShow rows and fold the rest behind a
// "Show N more" button. Only past collapseOver, so a button never hides a row
// or two; the filterable list of every visit is never folded.
const (
	collapseShow = 7
	collapseOver = 10
)

func htmlTable(headers []string, rows [][]string, classes []string, id string, rowAttrs []string) string {
	var b strings.Builder
	fold := rowAttrs == nil && len(rows) > collapseOver
	if fold {
		b.WriteString(`<div class="collapsible">`)
	}
	b.WriteString(`<div class="tablewrap"><table`)
	if id != "" {
		fmt.Fprintf(&b, ` id="%s"`, id)
	}
	b.WriteString("><tr>")
	for i, h := range headers {
		fmt.Fprintf(&b, `<th class="%s">%s</th>`, classes[i], esc(h))
	}
	b.WriteString("</tr>\n")
	for k, r := range rows {
		b.WriteString("<tr")
		if rowAttrs != nil {
			b.WriteString(rowAttrs[k])
		}
		if fold && k >= collapseShow {
			b.WriteString(` class="more"`)
		}
		b.WriteString(">")
		for i, c := range r {
			fmt.Fprintf(&b, `<td class="%s">%s</td>`, classes[i], c)
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</table></div>\n")
	if fold {
		n := len(rows) - collapseShow
		fmt.Fprintf(&b, `<button type="button" class="more-toggle" data-n="%d">Show %s more</button></div>`+"\n", n, fmtInt(n))
	}
	return b.String()
}

func tile(n, k string) string {
	return fmt.Sprintf(`<div class="tile"><div class="n">%s</div><div class="k">%s</div></div>`, esc(n), esc(k))
}

func htmlMonthGrid(m map[[2]int]int) string {
	lo, hi := yearRange(m)
	th := thresholds(m)
	var b strings.Builder
	b.WriteString(`<div class="tablewrap" style="padding:10px 12px"><table class="grid"><tr><td></td>`)
	for _, a := range monthAbbr {
		fmt.Fprintf(&b, "<td>%s</td>", a)
	}
	b.WriteString("<td></td></tr>\n")
	for y := hi; y >= lo; y-- {
		fmt.Fprintf(&b, `<tr><td class="y">%d</td>`, y)
		total := 0
		for mo := 1; mo <= 12; mo++ {
			n := m[[2]int{y, mo}]
			total += n
			plural := "s"
			if n == 1 {
				plural = ""
			}
			fmt.Fprintf(&b, `<td><div class="cell" style="background:var(--l%d)" title="%d visit%s in %s %d"></div></td>`,
				level(n, th), n, plural, monthAbbr[mo-1], y)
		}
		t := ""
		if total > 0 {
			t = fmt.Sprint(total)
		}
		fmt.Fprintf(&b, `<td class="t">%s</td></tr>`+"\n", t)
	}
	b.WriteString(`</table><div class="legend">less`)
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, `<i style="background:var(--l%d)"></i>`, i)
	}
	fmt.Fprintf(&b, `more <span style="margin-left:10px">(%s visits per month)</span></div></div>`,
		esc(strings.Join(bands(th)[1:], ", ")))
	return b.String()
}

// UserStatsHTML renders the full report.
func UserStatsHTML(s *UserStats) string {
	V, F := s.Visited, s.Favorites
	toTry := s.toTry()
	var h strings.Builder
	h.WriteString("<!doctype html><html lang='en'><head><meta charset='utf-8'>" +
		"<meta name='viewport' content='width=device-width, initial-scale=1'>" +
		"<title>Michelin stats</title><style>" + userStatsCSS + "</style></head><body><div class='wrap'>")
	h.WriteString("<h1>🍽️ Your Michelin restaurants</h1>")
	fmt.Fprintf(&h, "<p class='lede'>The restaurants you have visited or favorited. Generated %s.</p>",
		s.Generated.Format("2 Jan 2006"))
	countries := countBy(V, func(p UserPlace) string { return p.Country }, nil)
	h.WriteString(`<div class="tiles">` +
		tile(fmtInt(len(V)), "visited") +
		tile(fmtInt(s.starsCollected()), "stars collected") +
		tile(fmtInt(s.countWith(func(p UserPlace) bool { return p.Distinction == "3 Stars" })), "three-star") +
		tile(fmtInt(len(countries)), "countries") +
		tile(fmtInt(len(F)), "favorites") +
		tile(fmtInt(len(toTry)), "still to try") + "</div>")

	h.WriteString("<h2>By distinction</h2><p class='lede'>Each restaurant's current distinction, " +
		"measured against every restaurant in the guide that holds it.</p>")
	var rows [][]string
	maxV := 0
	for _, d := range s.distinctions() {
		if d.Visited > maxV {
			maxV = d.Visited
		}
	}
	for _, d := range s.distinctions() {
		rows = append(rows, []string{esc(d.Name), fmtInt(d.Visited), meter(d.Visited, maxV), fmtInt(d.InGuide),
			pctOf(d.Visited, d.InGuide), fmtInt(d.Favorite)})
	}
	h.WriteString(htmlTable([]string{"Distinction", "Visited", "", "In guide", "Share", "Favorites"},
		rows, []string{"", "n", "bar", "n", "n", "n"}, "", nil))
	var notes []string
	if gs := s.countWith(func(p UserPlace) bool { return p.GreenStar }); gs > 0 {
		notes = append(notes, fmt.Sprintf("%s visits hold a Green Star", fmtInt(gs)))
	}
	if gone := s.countWith(func(p UserPlace) bool { return !p.InGuide }); gone > 0 {
		notes = append(notes, fmt.Sprintf("%s are no longer in the guide", fmtInt(gone)))
	}
	if len(notes) > 0 {
		fmt.Fprintf(&h, "<p class='lede' style='margin-top:10px'>%s.</p>", esc(strings.Join(notes, "; ")))
	}

	h.WriteString(`<h2>Where</h2><div class="two"><div>`)
	byCountry := countBy(V, countryLabel, isStarred)
	rows = nil
	for _, r := range byCountry {
		rows = append(rows, []string{esc(r.Key), fmtInt(r.N), meter(r.N, byCountry[0].N), fmtInt(r.Sub)})
	}
	h.WriteString(htmlTable([]string{"Country", "Visited", "", "Starred"}, rows, []string{"", "n", "bar", "n"}, "", nil))
	h.WriteString("</div><div>")
	rows = nil
	for i, r := range countBy(V, func(p UserPlace) string { return p.Location }, isStarred) {
		if i == 12 {
			break
		}
		rows = append(rows, []string{esc(r.Key), fmtInt(r.N), coverage(r.Sub, s.StarredByLocation[r.Key])})
	}
	h.WriteString(htmlTable([]string{"City", "Visited", "Starred in guide"}, rows, []string{"", "n", "n"}, "", nil))
	h.WriteString("</div></div>")

	h.WriteString(`<h2>Cuisines and prices</h2><div class="two"><div>`)
	cuisines := countBy(V, func(p UserPlace) string { return p.Cuisine }, nil)
	rows = nil
	for i, r := range cuisines {
		if i == 15 {
			break
		}
		rows = append(rows, []string{esc(r.Key), fmtInt(r.N), meter(r.N, cuisines[0].N)})
	}
	h.WriteString(htmlTable([]string{"Cuisine", "Visited", ""}, rows, []string{"", "n", "bar"}, "", nil))
	h.WriteString("</div><div>")
	rows = nil
	for lvl := 4; lvl >= 1; lvl-- {
		n := s.countWith(func(p UserPlace) bool { return priceLevel(p.Price) == lvl })
		rows = append(rows, []string{strings.Repeat("$", lvl), fmtInt(n), meter(n, len(V))})
	}
	h.WriteString(htmlTable([]string{"Price level", "Visited", ""}, rows, []string{"", "n", "bar"}, "", nil))
	h.WriteString("<p class='lede' style='margin-top:8px'>Levels in any currency: €€€€ and ฿฿฿฿ count as $$$$.</p>")
	h.WriteString("</div></div>")

	if m := s.months(); len(m) > 0 {
		h.WriteString("<h2>By month</h2><p class='lede'>The date a restaurant was marked visited, " +
			"which may be later than the meal.</p>")
		h.WriteString(htmlMonthGrid(m))
	}

	h.WriteString("<h2>Favorites</h2>")
	fav := append([]UserPlace(nil), F...)
	sortByDistinction(fav)
	rows = nil
	for _, p := range fav {
		mark := "still to try"
		if p.Visited {
			mark = "visited " + esc(firstN(p.VisitedDate, 10))
		}
		rows = append(rows, []string{placeLink(p), esc(p.Location), esc(p.Distinction), mark})
	}
	h.WriteString(htmlTable([]string{"Restaurant", "City", "Distinction", ""}, rows, []string{"", "m", "", "m"}, "", nil))

	fmt.Fprintf(&h, "<h2>Every visit</h2><p class='lede'>%s visits, most recent first.</p>", fmtInt(len(V)))
	h.WriteString(`<input id="filter" class="filter" type="search" placeholder="Filter by name, city, cuisine or distinction…" autofocus>`)
	rows = nil
	var attrs []string
	for _, p := range V {
		name := placeLink(p)
		if p.Favorite {
			name += " ❤️"
		}
		if !p.InGuide {
			name += " 📜"
		}
		rows = append(rows, []string{name, esc(p.Location), esc(p.Cuisine), esc(p.Distinction), esc(firstN(p.VisitedDate, 10))})
		attrs = append(attrs, fmt.Sprintf(` data-s="%s"`, esc(strings.ToLower(strings.Join(
			[]string{p.Name, p.Location, p.Cuisine, p.Distinction, p.VisitedDate}, " ")))))
	}
	h.WriteString(htmlTable([]string{"Restaurant", "City", "Cuisine", "Distinction", "Visited"},
		rows, []string{"", "m", "m", "", "m"}, "all", attrs))

	fmt.Fprintf(&h, "<footer>alfred-michelin · generated %s</footer>", s.Generated.Format("2006-01-02 15:04"))
	h.WriteString("</div>" + userStatsJS + "</body></html>")
	return h.String()
}
