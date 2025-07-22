package main

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// US State mapping
var usStates = map[string]string{
	"AL": "Alabama", "AK": "Alaska", "AZ": "Arizona", "AR": "Arkansas", "CA": "California",
	"CO": "Colorado", "CT": "Connecticut", "DE": "Delaware", "FL": "Florida", "GA": "Georgia",
	"HI": "Hawaii", "ID": "Idaho", "IL": "Illinois", "IN": "Indiana", "IA": "Iowa",
	"KS": "Kansas", "KY": "Kentucky", "LA": "Louisiana", "ME": "Maine", "MD": "Maryland",
	"MA": "Massachusetts", "MI": "Michigan", "MN": "Minnesota", "MS": "Mississippi", "MO": "Missouri",
	"MT": "Montana", "NE": "Nebraska", "NV": "Nevada", "NH": "New Hampshire", "NJ": "New Jersey",
	"NM": "New Mexico", "NY": "New York", "NC": "North Carolina", "ND": "North Dakota", "OH": "Ohio",
	"OK": "Oklahoma", "OR": "Oregon", "PA": "Pennsylvania", "RI": "Rhode Island", "SC": "South Carolina",
	"SD": "South Dakota", "TN": "Tennessee", "TX": "Texas", "UT": "Utah", "VT": "Vermont",
	"VA": "Virginia", "WA": "Washington", "WV": "West Virginia", "WI": "Wisconsin", "WY": "Wyoming",
	"DC": "District of Columbia",
}

// Reverse mapping from full state names to abbreviations
var stateNamesToAbbr = make(map[string]string)

func init() {
	// Create reverse mapping
	for abbr, name := range usStates {
		stateNamesToAbbr[strings.ToLower(name)] = abbr
		stateNamesToAbbr[strings.ToLower(abbr)] = abbr
	}
}

// extractCountryAndState extracts country and US state from address and location
func extractCountryAndState(address, location string) (string, string) {
	// Normalize inputs
	address = strings.ToLower(address)
	location = strings.ToLower(location)

	// Check if it's in the USA
	isUSA := strings.Contains(address, "usa") || strings.Contains(location, "usa")

	if !isUSA {
		// For non-US addresses, extract country from location
		// Location format is typically "City, Country"
		parts := strings.Split(location, ",")
		if len(parts) >= 2 {
			country := strings.TrimSpace(parts[len(parts)-1])
			return country, ""
		}
		return "", ""
	}

	// For US addresses, extract state
	// Look for state abbreviations or full names in address
	addressWords := strings.Fields(address)

	for _, word := range addressWords {
		// Clean the word (remove punctuation)
		cleanWord := regexp.MustCompile(`[^a-zA-Z]`).ReplaceAllString(word, "")

		if cleanWord == "" {
			continue
		}

		// Check if it's a state abbreviation or name
		if stateAbbr, exists := stateNamesToAbbr[cleanWord]; exists {
			return "USA", stateAbbr
		}
	}

	// If no state found in address, try to extract from location
	// Location format for US is typically "City, State"
	locationParts := strings.Split(location, ",")
	if len(locationParts) >= 2 {
		statePart := strings.TrimSpace(locationParts[len(locationParts)-2]) // Second to last part
		if stateAbbr, exists := stateNamesToAbbr[statePart]; exists {
			return "USA", stateAbbr
		}
	}

	return "USA", ""
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run add_location_columns.go <database_path>")
		os.Exit(1)
	}

	dbPath := os.Args[1]

	// Open database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		fmt.Printf("Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Check if columns already exist
	var hasCountry, hasState bool
	rows, err := db.Query("PRAGMA table_info(restaurants)")
	if err != nil {
		fmt.Printf("Error checking table info: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, dataType string
		var notnull, pk int
		var defaultValue sql.NullString

		err := rows.Scan(&cid, &name, &dataType, &notnull, &defaultValue, &pk)
		if err != nil {
			fmt.Printf("Error scanning table info: %v\n", err)
			os.Exit(1)
		}

		if name == "country" {
			hasCountry = true
		}
		if name == "us_state" {
			hasState = true
		}
	}

	// Add columns if they don't exist
	if !hasCountry {
		_, err = db.Exec("ALTER TABLE restaurants ADD COLUMN country TEXT")
		if err != nil {
			fmt.Printf("Error adding country column: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Added country column")
	}

	if !hasState {
		_, err = db.Exec("ALTER TABLE restaurants ADD COLUMN us_state TEXT")
		if err != nil {
			fmt.Printf("Error adding us_state column: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Added us_state column")
	}

	// Get all restaurants
	rows, err = db.Query("SELECT id, address, location FROM restaurants")
	if err != nil {
		fmt.Printf("Error querying restaurants: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	updated := 0
	usRestaurants := 0

	for rows.Next() {
		var id int64
		var address, location sql.NullString

		err := rows.Scan(&id, &address, &location)
		if err != nil {
			fmt.Printf("Error scanning restaurant: %v\n", err)
			continue
		}

		var addrStr, locStr string
		if address.Valid {
			addrStr = address.String
		}
		if location.Valid {
			locStr = location.String
		}

		country, state := extractCountryAndState(addrStr, locStr)

		// Update the restaurant
		_, err = db.Exec("UPDATE restaurants SET country = ?, us_state = ? WHERE id = ?",
			country, state, id)
		if err != nil {
			fmt.Printf("Error updating restaurant %d: %v\n", id, err)
			continue
		}

		updated++
		if country == "USA" {
			usRestaurants++
		}

		// Print progress every 100 restaurants
		if updated%100 == 0 {
			fmt.Printf("Processed %d restaurants...\n", updated)
		}
	}

	fmt.Printf("\nUpdate complete!\n")
	fmt.Printf("Total restaurants processed: %d\n", updated)
	fmt.Printf("US restaurants found: %d\n", usRestaurants)

	// Show some statistics
	fmt.Printf("\nSample results:\n")
	sampleRows, err := db.Query(`
		SELECT name, address, location, country, us_state 
		FROM restaurants 
		WHERE country = 'USA' AND us_state != '' 
		LIMIT 10
	`)
	if err != nil {
		fmt.Printf("Error querying sample results: %v\n", err)
		return
	}
	defer sampleRows.Close()

	for sampleRows.Next() {
		var name, address, location, country, state sql.NullString
		err := sampleRows.Scan(&name, &address, &location, &country, &state)
		if err != nil {
			continue
		}

		fmt.Printf("  %s: %s, %s\n",
			name.String,
			country.String,
			state.String)
	}
}
