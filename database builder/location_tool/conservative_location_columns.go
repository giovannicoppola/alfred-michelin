package main

import (
	"database/sql"
	"fmt"
	"os"
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
			// Capitalize first letter
			if len(country) > 0 {
				country = strings.ToUpper(country[:1]) + country[1:]
			}
			return country, ""
		}
		return "", ""
	}

	// For US addresses, ONLY extract state from location field (more reliable)
	// Location format for US is typically "City, State"
	locationParts := strings.Split(location, ",")
	if len(locationParts) >= 2 {
		// Try the second to last part (before "USA")
		statePart := strings.TrimSpace(locationParts[len(locationParts)-2])
		if stateAbbr, exists := stateNamesToAbbr[statePart]; exists {
			return "USA", stateAbbr
		}
	}

	return "USA", ""
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run conservative_location_columns.go <database_path>")
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

	// Get all restaurants
	rows, err := db.Query("SELECT id, address, location FROM restaurants")
	if err != nil {
		fmt.Printf("Error querying restaurants: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	updated := 0
	usRestaurants := 0
	usWithState := 0

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
			if state != "" {
				usWithState++
			}
		}

		// Print progress every 1000 restaurants
		if updated%1000 == 0 {
			fmt.Printf("Processed %d restaurants...\n", updated)
		}
	}

	fmt.Printf("\nUpdate complete!\n")
	fmt.Printf("Total restaurants processed: %d\n", updated)
	fmt.Printf("US restaurants found: %d\n", usRestaurants)
	fmt.Printf("US restaurants with state: %d\n", usWithState)

	// Show some statistics
	fmt.Printf("\nSample US results:\n")
	sampleRows, err := db.Query(`
		SELECT name, address, location, country, us_state 
		FROM restaurants 
		WHERE country = 'USA' AND us_state != '' 
		ORDER BY us_state
		LIMIT 15
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

	// Show some non-US results
	fmt.Printf("\nSample non-US results:\n")
	sampleRows2, err := db.Query(`
		SELECT name, address, location, country, us_state 
		FROM restaurants 
		WHERE country != 'USA' AND country != '' 
		LIMIT 10
	`)
	if err != nil {
		fmt.Printf("Error querying sample results: %v\n", err)
		return
	}
	defer sampleRows2.Close()

	for sampleRows2.Next() {
		var name, address, location, country, state sql.NullString
		err := sampleRows2.Scan(&name, &address, &location, &country, &state)
		if err != nil {
			continue
		}

		fmt.Printf("  %s: %s\n",
			name.String,
			country.String)
	}
}
