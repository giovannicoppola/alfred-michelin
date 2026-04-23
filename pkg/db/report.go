package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// GenerateReport returns a markdown-formatted snapshot of the database:
// totals, distinction distribution, per-year counts, and top countries /
// US states / cuisines. Intended for the `report` subcommand.
func GenerateReport(db *sql.DB) (string, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "# Michelin Guide Database Report\n\n")
	fmt.Fprintf(&b, "_Generated: %s_\n\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))

	if err := writeSummary(&b, db); err != nil {
		return "", fmt.Errorf("summary: %w", err)
	}
	if err := writeCurrentDistinctions(&b, db); err != nil {
		return "", fmt.Errorf("current distinctions: %w", err)
	}
	if err := writeAwardsByYear(&b, db); err != nil {
		return "", fmt.Errorf("awards by year: %w", err)
	}
	if err := writeGreenStarsByYear(&b, db); err != nil {
		return "", fmt.Errorf("green stars by year: %w", err)
	}
	if err := writeTopCountries(&b, db); err != nil {
		return "", fmt.Errorf("top countries: %w", err)
	}
	if err := writeTopUSStates(&b, db); err != nil {
		return "", fmt.Errorf("top US states: %w", err)
	}
	if err := writeTopCuisines(&b, db); err != nil {
		return "", fmt.Errorf("top cuisines: %w", err)
	}
	if err := writeUserStats(&b, db); err != nil {
		return "", fmt.Errorf("user stats: %w", err)
	}

	return b.String(), nil
}

func writeSummary(b *strings.Builder, db *sql.DB) error {
	var total, inGuide, awards, greenStars, minYear, maxYear int
	if err := db.QueryRow("SELECT COUNT(*) FROM restaurants").Scan(&total); err != nil {
		return err
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM restaurants WHERE in_guide = 1").Scan(&inGuide); err != nil {
		return err
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM restaurant_awards").Scan(&awards); err != nil {
		return err
	}
	// Distinct in-guide restaurants with any green-star award on record.
	// Not filtered to the latest year because Michelin's 2026 detail pages
	// don't expose the green-star attribute, so a "latest year only" filter
	// would undercount known recipients.
	err := db.QueryRow(`
		SELECT COUNT(DISTINCT ra.restaurant_id)
		FROM restaurant_awards ra
		JOIN restaurants r ON r.id = ra.restaurant_id
		WHERE ra.green_star = 1 AND r.in_guide = 1
	`).Scan(&greenStars)
	if err != nil {
		return err
	}
	if err := db.QueryRow("SELECT MIN(year), MAX(year) FROM restaurant_awards").Scan(&minYear, &maxYear); err != nil {
		return err
	}

	fmt.Fprintf(b, "## Summary\n\n")
	fmt.Fprintf(b, "| Metric | Value |\n")
	fmt.Fprintf(b, "| --- | ---: |\n")
	fmt.Fprintf(b, "| Total restaurants | %s |\n", fmtInt(total))
	fmt.Fprintf(b, "| Currently in guide | %s (%.1f%%) |\n", fmtInt(inGuide), pct(inGuide, total))
	fmt.Fprintf(b, "| Removed from guide | %s (%.1f%%) |\n", fmtInt(total-inGuide), pct(total-inGuide, total))
	fmt.Fprintf(b, "| Award rows (all years) | %s |\n", fmtInt(awards))
	fmt.Fprintf(b, "| Green star recipients (in guide, any year) | %s |\n", fmtInt(greenStars))
	fmt.Fprintf(b, "| Years covered | %d – %d |\n\n", minYear, maxYear)
	return nil
}

func writeCurrentDistinctions(b *strings.Builder, db *sql.DB) error {
	rows, err := db.Query(`
		SELECT ra.distinction, COUNT(*)
		FROM restaurant_awards ra
		JOIN (
			SELECT restaurant_id, MAX(year) AS max_year
			FROM restaurant_awards GROUP BY restaurant_id
		) latest
		  ON latest.restaurant_id = ra.restaurant_id AND latest.max_year = ra.year
		JOIN restaurants r ON r.id = ra.restaurant_id
		WHERE r.in_guide = 1
		GROUP BY ra.distinction
		ORDER BY CASE ra.distinction
		    WHEN '3 Stars' THEN 1
		    WHEN '2 Stars' THEN 2
		    WHEN '1 Star' THEN 3
		    WHEN 'Bib Gourmand' THEN 4
		    WHEN 'Selected Restaurants' THEN 5
		    ELSE 6
		END
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Fprintf(b, "## Current distinctions (restaurants in guide)\n\n")
	fmt.Fprintf(b, "| Distinction | Count |\n| --- | ---: |\n")
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return err
		}
		fmt.Fprintf(b, "| %s | %s |\n", d, fmtInt(n))
	}
	fmt.Fprintln(b)
	return rows.Err()
}

func writeAwardsByYear(b *strings.Builder, db *sql.DB) error {
	rows, err := db.Query(`
		SELECT year, COUNT(*) FROM restaurant_awards
		GROUP BY year ORDER BY year DESC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Fprintf(b, "## Awards by year\n\n")
	fmt.Fprintf(b, "| Year | Awards |\n| ---: | ---: |\n")
	for rows.Next() {
		var year, n int
		if err := rows.Scan(&year, &n); err != nil {
			return err
		}
		fmt.Fprintf(b, "| %d | %s |\n", year, fmtInt(n))
	}
	fmt.Fprintln(b)
	return rows.Err()
}

func writeGreenStarsByYear(b *strings.Builder, db *sql.DB) error {
	rows, err := db.Query(`
		SELECT year, COUNT(*) FROM restaurant_awards
		WHERE green_star = 1
		GROUP BY year ORDER BY year DESC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Fprintf(b, "## Green stars by year\n\n")
	fmt.Fprintf(b, "| Year | Green Stars |\n| ---: | ---: |\n")
	any := false
	for rows.Next() {
		var year, n int
		if err := rows.Scan(&year, &n); err != nil {
			return err
		}
		fmt.Fprintf(b, "| %d | %s |\n", year, fmtInt(n))
		any = true
	}
	if !any {
		fmt.Fprintf(b, "| _(none recorded)_ | |\n")
	}
	fmt.Fprintln(b)
	return rows.Err()
}

func writeTopCountries(b *strings.Builder, db *sql.DB) error {
	// Restaurants broken down by distinction, using each restaurant's most
	// recent award. Only in-guide restaurants are counted.
	rows, err := db.Query(`
		SELECT
		    COALESCE(NULLIF(r.country, ''), '(unknown)') AS country,
		    COUNT(*) AS total,
		    SUM(CASE WHEN ra.distinction = '3 Stars' THEN 1 ELSE 0 END) AS s3,
		    SUM(CASE WHEN ra.distinction = '2 Stars' THEN 1 ELSE 0 END) AS s2,
		    SUM(CASE WHEN ra.distinction = '1 Star' THEN 1 ELSE 0 END) AS s1,
		    SUM(CASE WHEN ra.distinction = 'Bib Gourmand' THEN 1 ELSE 0 END) AS bg,
		    SUM(CASE WHEN ra.distinction = 'Selected Restaurants' THEN 1 ELSE 0 END) AS sel
		FROM restaurants r
		JOIN restaurant_awards ra ON ra.restaurant_id = r.id
		JOIN (
			SELECT restaurant_id, MAX(year) AS max_year
			FROM restaurant_awards GROUP BY restaurant_id
		) latest
		  ON latest.restaurant_id = ra.restaurant_id AND latest.max_year = ra.year
		WHERE r.in_guide = 1
		GROUP BY country
		ORDER BY total DESC
		LIMIT 20
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Fprintf(b, "## Top 20 countries (current guide)\n\n")
	fmt.Fprintf(b, "| Country | Restaurants | 3 ⭐ | 2 ⭐ | 1 ⭐ | Bib | Sel |\n")
	fmt.Fprintf(b, "| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for rows.Next() {
		var country string
		var total, s3, s2, s1, bg, sel int
		if err := rows.Scan(&country, &total, &s3, &s2, &s1, &bg, &sel); err != nil {
			return err
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			country, fmtInt(total), fmtInt(s3), fmtInt(s2), fmtInt(s1), fmtInt(bg), fmtInt(sel))
	}
	fmt.Fprintln(b)
	return rows.Err()
}

func writeTopUSStates(b *strings.Builder, db *sql.DB) error {
	rows, err := db.Query(`
		SELECT
		    COALESCE(NULLIF(r.us_state, ''), '(unknown)') AS st,
		    COUNT(*) AS total,
		    SUM(CASE WHEN ra.distinction = '3 Stars' THEN 1 ELSE 0 END) AS s3,
		    SUM(CASE WHEN ra.distinction = '2 Stars' THEN 1 ELSE 0 END) AS s2,
		    SUM(CASE WHEN ra.distinction = '1 Star' THEN 1 ELSE 0 END) AS s1,
		    SUM(CASE WHEN ra.distinction = 'Bib Gourmand' THEN 1 ELSE 0 END) AS bg,
		    SUM(CASE WHEN ra.distinction = 'Selected Restaurants' THEN 1 ELSE 0 END) AS sel
		FROM restaurants r
		JOIN restaurant_awards ra ON ra.restaurant_id = r.id
		JOIN (
			SELECT restaurant_id, MAX(year) AS max_year
			FROM restaurant_awards GROUP BY restaurant_id
		) latest
		  ON latest.restaurant_id = ra.restaurant_id AND latest.max_year = ra.year
		WHERE r.in_guide = 1 AND r.country = 'USA'
		GROUP BY st
		ORDER BY total DESC
		LIMIT 20
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Fprintf(b, "## Top 20 US states (current guide)\n\n")
	fmt.Fprintf(b, "| State | Restaurants | 3 ⭐ | 2 ⭐ | 1 ⭐ | Bib | Sel |\n")
	fmt.Fprintf(b, "| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for rows.Next() {
		var st string
		var total, s3, s2, s1, bg, sel int
		if err := rows.Scan(&st, &total, &s3, &s2, &s1, &bg, &sel); err != nil {
			return err
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			st, fmtInt(total), fmtInt(s3), fmtInt(s2), fmtInt(s1), fmtInt(bg), fmtInt(sel))
	}
	fmt.Fprintln(b)
	return rows.Err()
}

func writeTopCuisines(b *strings.Builder, db *sql.DB) error {
	rows, err := db.Query(`
		SELECT COALESCE(NULLIF(r.cuisine, ''), '(unknown)') AS cuisine, COUNT(*) AS n
		FROM restaurants r WHERE r.in_guide = 1
		GROUP BY cuisine ORDER BY n DESC LIMIT 20
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Fprintf(b, "## Top 20 cuisines (current guide)\n\n")
	fmt.Fprintf(b, "| Cuisine | Count |\n| --- | ---: |\n")
	for rows.Next() {
		var c string
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			return err
		}
		fmt.Fprintf(b, "| %s | %s |\n", c, fmtInt(n))
	}
	fmt.Fprintln(b)
	return rows.Err()
}

func writeUserStats(b *strings.Builder, db *sql.DB) error {
	var favs, visits int
	// These tables are created lazily by PreserveUserDataDuringUpdate, so
	// they may not exist on a freshly-shipped DB. Treat "no such table" as
	// zero rather than an error.
	if err := db.QueryRow("SELECT COUNT(*) FROM user_favorites").Scan(&favs); err != nil {
		if !strings.Contains(err.Error(), "no such table") {
			return err
		}
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM user_visits").Scan(&visits); err != nil {
		if !strings.Contains(err.Error(), "no such table") {
			return err
		}
	}
	if favs == 0 && visits == 0 {
		return nil
	}
	fmt.Fprintf(b, "## Your personal stats\n\n")
	fmt.Fprintf(b, "| Metric | Value |\n| --- | ---: |\n")
	fmt.Fprintf(b, "| Favorites | %s |\n", fmtInt(favs))
	fmt.Fprintf(b, "| Visits | %s |\n\n", fmtInt(visits))
	return nil
}

// fmtInt formats an integer with comma thousands separators.
func fmtInt(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 && n >= 0 {
		return s
	}
	neg := ""
	if n < 0 {
		neg = "-"
		s = s[1:]
	}
	var out strings.Builder
	out.Grow(len(s) + len(s)/3)
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(r)
	}
	return neg + out.String()
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(n) / float64(total)
}
