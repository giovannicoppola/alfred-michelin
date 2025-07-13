package main

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"unicode"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// normalizeForSearch removes accents and converts to lowercase for search comparison
func normalizeForSearch(text string) string {
	// First normalize to NFD (decomposed form)
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	normalized, _, _ := transform.String(t, text)
	return strings.ToLower(normalized)
}

func main() {
	// Open database
	db, err := sql.Open("sqlite3", "data/michelin.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Count restaurants that need normalization
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM restaurants WHERE name_normalized IS NULL OR name_normalized = ''").Scan(&count)
	if err != nil {
		log.Fatalf("Failed to count restaurants: %v", err)
	}

	fmt.Printf("Found %d restaurants that need normalized fields populated\n", count)

	if count == 0 {
		fmt.Println("All restaurants already have normalized fields!")
		return
	}

	// Get restaurants that need normalization
	rows, err := db.Query("SELECT id, name, location, cuisine FROM restaurants WHERE name_normalized IS NULL OR name_normalized = ''")
	if err != nil {
		log.Fatalf("Failed to query restaurants: %v", err)
	}
	defer rows.Close()

	updated := 0
	for rows.Next() {
		var id int64
		var name, location, cuisine sql.NullString

		err := rows.Scan(&id, &name, &location, &cuisine)
		if err != nil {
			log.Printf("Failed to scan restaurant %d: %v", id, err)
			continue
		}

		// Normalize the text fields
		var nameNorm, locationNorm, cuisineNorm sql.NullString

		if name.Valid {
			nameNorm = sql.NullString{String: normalizeForSearch(name.String), Valid: true}
		}
		if location.Valid {
			locationNorm = sql.NullString{String: normalizeForSearch(location.String), Valid: true}
		}
		if cuisine.Valid {
			cuisineNorm = sql.NullString{String: normalizeForSearch(cuisine.String), Valid: true}
		}

		// Update the restaurant with normalized values
		_, err = db.Exec(`
			UPDATE restaurants 
			SET name_normalized = ?, location_normalized = ?, cuisine_normalized = ?
			WHERE id = ?
		`, nameNorm, locationNorm, cuisineNorm, id)
		if err != nil {
			log.Printf("Failed to update normalized columns for restaurant %d: %v", id, err)
			continue
		}

		updated++
		if updated%100 == 0 {
			fmt.Printf("Updated %d restaurants...\n", updated)
		}
	}

	fmt.Printf("Successfully updated normalized fields for %d restaurants\n", updated)
}
