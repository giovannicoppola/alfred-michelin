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

// cityToCountry maps single-token locations (city-states, or cities Michelin
// lists without the country suffix) to their country. Michelin's location
// field is usually "City, Country" — but for Dubai, Singapore, Macau etc. it
// is just the city. Without this table those rows end up with an empty
// country field.
var cityToCountry = map[string]string{
	"dubai":            "UAE",
	"abu dhabi":        "UAE",
	"macau":            "Macau",
	"singapore":        "Singapore",
	"hong kong":        "Hong Kong",
	"tokyo":            "Japan",
	"osaka":            "Japan",
	"kyoto":            "Japan",
	"seoul":            "South Korea",
	"bangkok":          "Thailand",
	"manila":           "Philippines",
	"jakarta":          "Indonesia",
	"kuala lumpur":     "Malaysia",
	"ho chi minh city": "Vietnam",
	"hanoi":            "Vietnam",
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

	// Country is Michelin's final comma-separated segment. A plain substring
	// scan would false-positive on tokens like "Kusagawacho" (Kyoto) which
	// contain "usa" mid-word, so only treat a restaurant as US when the last
	// segment of either location or address matches a recognized US token.
	// Michelin uses both "USA" and "United States" across the dataset.
	lastSegment := func(s string) string {
		parts := strings.Split(s, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	isUSToken := func(s string) bool {
		return s == "usa" || s == "united states" || s == "united states of america"
	}
	isUSA := isUSToken(lastSegment(address)) || isUSToken(lastSegment(location))

	if !isUSA {
		// For non-US addresses, extract country from location.
		// Location format is typically "City, Country", but Michelin uses a
		// bare city name for city-states (Dubai, Singapore, Macau, Hong Kong)
		// and other single-city locations (Paris, Luxembourg, London). When
		// the location is a single token, fall back to the address's last
		// comma-separated segment, which Michelin always fills with the
		// country.
		// capitalize title-cases a country string that was lowercased upstream.
		// A naive "uppercase first letter only" approach produced values like
		// "United kingdom" / "South korea" in 2025 — per-word casing keeps the
		// report and search surfaces readable.
		abbrev := map[string]string{
			"usa": "USA", "uae": "UAE", "uk": "UK",
			"sar": "SAR", // Hong Kong SAR, Macau SAR
		}
		capitalize := func(s string) string {
			s = strings.TrimSpace(s)
			if s == "" {
				return s
			}
			if v, ok := abbrev[strings.ToLower(s)]; ok {
				return v
			}
			words := strings.Fields(s)
			for i, w := range words {
				lw := strings.ToLower(w)
				if up, ok := abbrev[lw]; ok {
					words[i] = up
					continue
				}
				// Preserve connector words ("of", "and", "&") in lowercase
				// except at the start of the phrase.
				if i > 0 && (lw == "of" || lw == "and" || lw == "the") {
					words[i] = lw
					continue
				}
				if len(w) > 0 {
					words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
				}
			}
			return strings.Join(words, " ")
		}
		locTrimmed := strings.TrimSpace(location)
		if country, ok := cityToCountry[locTrimmed]; ok {
			return country, ""
		}
		if parts := strings.Split(location, ","); len(parts) >= 2 {
			return capitalize(strings.TrimSpace(parts[len(parts)-1])), ""
		}
		if parts := strings.Split(address, ","); len(parts) >= 2 {
			country := strings.TrimSpace(parts[len(parts)-1])
			if country != "" {
				return capitalize(country), ""
			}
		}
		return "", ""
	}

	// For US addresses, the format may be either
	//   "Street, City, ZIP, USA"            (older Michelin layout)
	//   "Street, City, ST, ZIP, USA"        (current Michelin layout — state included)
	// Walk the comma-separated parts in order: prefer an explicit state
	// abbreviation when Michelin gives us one, fall back to deriving the
	// state from a 5-digit ZIP code found anywhere in the address.
	addressParts := strings.Split(address, ",")

	for _, part := range addressParts {
		token := strings.ToUpper(strings.TrimSpace(part))
		if len(token) == 2 {
			if _, ok := zipCodeRanges[token]; ok {
				return "USA", token
			}
		}
	}

	zipOnly := regexp.MustCompile(`^\d{5}(-\d{4})?$`)
	for _, part := range addressParts {
		token := strings.TrimSpace(part)
		if !zipOnly.MatchString(token) {
			continue
		}
		if state := getStateFromZIP(token); state != "" {
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
