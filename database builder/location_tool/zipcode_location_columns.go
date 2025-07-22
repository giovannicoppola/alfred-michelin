package main

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// ZIP code ranges for US states
var zipCodeRanges = map[string][]int{
	"AL": {35000, 36999}, // Alabama
	"AK": {99500, 99999}, // Alaska
	"AZ": {85000, 86999}, // Arizona
	"AR": {71600, 72999}, // Arkansas
	"CA": {90000, 96199}, // California
	"CO": {80000, 81999}, // Colorado
	"CT": {6000, 6999},   // Connecticut
	"DE": {19700, 19999}, // Delaware
	"FL": {32000, 34999}, // Florida
	"GA": {30000, 31999}, // Georgia
	"HI": {96700, 96999}, // Hawaii
	"ID": {83200, 83999}, // Idaho
	"IL": {60000, 62999}, // Illinois
	"IN": {46000, 47999}, // Indiana
	"IA": {50000, 52999}, // Iowa
	"KS": {66000, 67999}, // Kansas
	"KY": {40000, 42999}, // Kentucky
	"LA": {70000, 71499}, // Louisiana
	"ME": {3900, 4999},   // Maine
	"MD": {20300, 21999}, // Maryland
	"MA": {1000, 2799},   // Massachusetts
	"MI": {48000, 49999}, // Michigan
	"MN": {55000, 56999}, // Minnesota
	"MS": {38600, 39799}, // Mississippi
	"MO": {63000, 65999}, // Missouri
	"MT": {59000, 59999}, // Montana
	"NE": {68000, 69999}, // Nebraska
	"NV": {88900, 89999}, // Nevada
	"NH": {3000, 3899},   // New Hampshire
	"NJ": {7000, 8999},   // New Jersey
	"NM": {87000, 88499}, // New Mexico
	"NY": {10000, 14999}, // New York
	"NC": {27000, 28999}, // North Carolina
	"ND": {58000, 58899}, // North Dakota
	"OH": {43000, 45999}, // Ohio
	"OK": {73000, 74999}, // Oklahoma
	"OR": {97000, 97999}, // Oregon
	"PA": {15000, 19699}, // Pennsylvania
	"RI": {2800, 2999},   // Rhode Island
	"SC": {29000, 29999}, // South Carolina
	"SD": {57000, 57799}, // South Dakota
	"TN": {37000, 38599}, // Tennessee
	"TX": {75000, 79999}, // Texas
	"UT": {84000, 84999}, // Utah
	"VT": {5000, 5999},   // Vermont
	"VA": {20100, 24699}, // Virginia
	"WA": {98000, 99499}, // Washington
	"WV": {24700, 26899}, // West Virginia
	"WI": {53000, 54999}, // Wisconsin
	"WY": {82000, 83199}, // Wyoming
	"DC": {20000, 20799}, // District of Columbia
}

func init() {
	// Add some additional ranges for states that span multiple ranges
	zipCodeRanges["CA"] = append(zipCodeRanges["CA"], 90000, 96199) // California has multiple ranges
	zipCodeRanges["NY"] = append(zipCodeRanges["NY"], 10000, 14999) // New York has multiple ranges
}

// getStateFromZIP returns the state abbreviation for a given ZIP code
func getStateFromZIP(zipStr string) string {
	// Clean the ZIP code
	zipClean := regexp.MustCompile(`[^0-9]`).ReplaceAllString(zipStr, "")
	if len(zipClean) < 5 {
		return ""
	}

	// Take first 5 digits
	zipCode, err := strconv.Atoi(zipClean[:5])
	if err != nil {
		return ""
	}

	// Check each state's ZIP range
	for state, ranges := range zipCodeRanges {
		for i := 0; i < len(ranges); i += 2 {
			if i+1 < len(ranges) {
				start := ranges[i]
				end := ranges[i+1]
				if zipCode >= start && zipCode <= end {
					return state
				}
			}
		}
	}

	return ""
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

	// For US addresses, extract state from ZIP code in address
	// Address format is typically "Street, City, ZIP, USA"
	addressParts := strings.Split(address, ",")
	if len(addressParts) >= 3 {
		// The ZIP code is usually the third part
		zipPart := strings.TrimSpace(addressParts[2])
		state := getStateFromZIP(zipPart)
		if state != "" {
			return "USA", state
		}
	}

	return "USA", ""
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run zipcode_location_columns.go <database_path>")
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
