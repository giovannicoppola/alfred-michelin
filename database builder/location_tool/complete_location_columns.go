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

// Common street words that might be confused with state abbreviations
var streetWords = map[string]bool{
	"street": true, "st": true, "avenue": true, "ave": true, "road": true, "rd": true,
	"boulevard": true, "blvd": true, "drive": true, "dr": true, "lane": true, "ln": true,
	"court": true, "ct": true, "place": true, "pl": true, "way": true, "circle": true,
	"terrace": true, "ter": true, "highway": true, "hwy": true, "parkway": true, "pkwy": true,
}

// ZIP code ranges for US states
var zipCodeRanges = map[string][]int{
	"AL": {35000, 36999}, "AK": {99500, 99999}, "AZ": {85000, 86999}, "AR": {71600, 72999},
	"CA": {90000, 96199}, "CO": {80000, 81999}, "CT": {6000, 6999}, "DE": {19700, 19999},
	"FL": {32000, 34999}, "GA": {30000, 31999}, "HI": {96700, 96999}, "ID": {83200, 83999},
	"IL": {60000, 62999}, "IN": {46000, 47999}, "IA": {50000, 52999}, "KS": {66000, 67999},
	"KY": {40000, 42999}, "LA": {70000, 71499}, "ME": {3900, 4999}, "MD": {20300, 21999},
	"MA": {1000, 2799}, "MI": {48000, 49999}, "MN": {55000, 56999}, "MS": {38600, 39799},
	"MO": {63000, 65999}, "MT": {59000, 59999}, "NE": {68000, 69999}, "NV": {88900, 89999},
	"NH": {3000, 3899}, "NJ": {7000, 8999}, "NM": {87000, 88499}, "NY": {10000, 14999},
	"NC": {27000, 28999}, "ND": {58000, 58899}, "OH": {43000, 45999}, "OK": {73000, 74999},
	"OR": {97000, 97999}, "PA": {15000, 19699}, "RI": {2800, 2999}, "SC": {29000, 29999},
	"SD": {57000, 57799}, "TN": {37000, 38599}, "TX": {75000, 79999}, "UT": {84000, 84999},
	"VT": {5000, 5999}, "VA": {20100, 24699}, "WA": {98000, 99499}, "WV": {24700, 26899},
	"WI": {53000, 54999}, "WY": {82000, 83199}, "DC": {20000, 20799},
}

// City to country mapping for normalized locations
var cityToCountry = map[string]string{
	"dubai":            "UAE",
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
	"phnom penh":       "Cambodia",
	"vientiane":        "Laos",
	"yangon":           "Myanmar",
	"dhaka":            "Bangladesh",
	"kathmandu":        "Nepal",
	"colombo":          "Sri Lanka",
	"male":             "Maldives",
	"thimphu":          "Bhutan",
	"paro":             "Bhutan",
	"tashkent":         "Uzbekistan",
	"astana":           "Kazakhstan",
	"bishkek":          "Kyrgyzstan",
	"dushanbe":         "Tajikistan",
	"ashgabat":         "Turkmenistan",
}

func init() {
	for abbr, name := range usStates {
		stateNamesToAbbr[strings.ToLower(name)] = abbr
		stateNamesToAbbr[strings.ToLower(abbr)] = abbr
	}
}

func getStateFromZIP(zipStr string) string {
	zipClean := regexp.MustCompile(`[^0-9]`).ReplaceAllString(zipStr, "")
	if len(zipClean) < 5 {
		return ""
	}
	zipCode, err := strconv.Atoi(zipClean[:5])
	if err != nil {
		return ""
	}
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

// extractCountryAndState uses location_normalized, location, and address for best guess
func extractCountryAndState(address, location, locationNorm string) (string, string) {
	address = strings.ToLower(address)
	location = strings.ToLower(location)
	locationNorm = strings.ToLower(locationNorm)

	// 1. US logic
	isUSA := strings.Contains(address, "usa") || strings.Contains(location, "usa") || strings.Contains(locationNorm, "usa")
	if isUSA {
		addressParts := strings.Split(address, ",")
		if len(addressParts) >= 3 {
			zipPart := strings.TrimSpace(addressParts[2])
			state := getStateFromZIP(zipPart)
			if state != "" {
				return "USA", state
			}
		}
		return "USA", ""
	}

	// 2. Try location_normalized for city-to-country mapping
	if country, ok := cityToCountry[locationNorm]; ok {
		return country, ""
	}

	// 3. Try location_normalized for country (format: "city, country")
	parts := strings.Split(locationNorm, ",")
	if len(parts) >= 2 {
		country := strings.TrimSpace(parts[len(parts)-1])
		if country != "" && country != locationNorm {
			country = strings.ToUpper(country[:1]) + country[1:]
			return country, ""
		}
	}

	// 4. Try location for country (format: "city, country")
	parts = strings.Split(location, ",")
	if len(parts) >= 2 {
		country := strings.TrimSpace(parts[len(parts)-1])
		if country != "" && country != location {
			country = strings.ToUpper(country[:1]) + country[1:]
			return country, ""
		}
	}

	// 5. Try address for country (last part, not a number)
	addressParts := strings.Split(address, ",")
	if len(addressParts) >= 2 {
		lastPart := strings.TrimSpace(addressParts[len(addressParts)-1])
		if len(lastPart) > 0 && len(lastPart) < 20 && !regexp.MustCompile(`^\d+$`).MatchString(lastPart) {
			lastPart = strings.ToUpper(lastPart[:1]) + lastPart[1:]
			return lastPart, ""
		}
	}

	return "", ""
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run complete_location_columns.go <database_path>")
		os.Exit(1)
	}
	dbPath := os.Args[1]
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		fmt.Printf("Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	rows, err := db.Query("SELECT id, address, location, location_normalized FROM restaurants")
	if err != nil {
		fmt.Printf("Error querying restaurants: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()
	updated := 0
	usRestaurants := 0
	usWithState := 0
	withCountry := 0
	for rows.Next() {
		var id int64
		var address, location, locationNorm sql.NullString
		err := rows.Scan(&id, &address, &location, &locationNorm)
		if err != nil {
			fmt.Printf("Error scanning restaurant: %v\n", err)
			continue
		}
		var addrStr, locStr, locNormStr string
		if address.Valid {
			addrStr = address.String
		}
		if location.Valid {
			locStr = location.String
		}
		if locationNorm.Valid {
			locNormStr = locationNorm.String
		}
		country, state := extractCountryAndState(addrStr, locStr, locNormStr)
		_, err = db.Exec("UPDATE restaurants SET country = ?, us_state = ? WHERE id = ?", country, state, id)
		if err != nil {
			fmt.Printf("Error updating restaurant %d: %v\n", id, err)
			continue
		}
		updated++
		if country != "" {
			withCountry++
		}
		if country == "USA" {
			usRestaurants++
			if state != "" {
				usWithState++
			}
		}
		if updated%1000 == 0 {
			fmt.Printf("Processed %d restaurants...\n", updated)
		}
	}
	fmt.Printf("\nUpdate complete!\n")
	fmt.Printf("Total restaurants processed: %d\n", updated)
	fmt.Printf("Restaurants with country: %d\n", withCountry)
	fmt.Printf("US restaurants found: %d\n", usRestaurants)
	fmt.Printf("US restaurants with state: %d\n", usWithState)
	fmt.Printf("\nSample results with country:\n")
	sampleRows, err := db.Query(`SELECT name, address, location, country, us_state FROM restaurants WHERE country != '' ORDER BY country LIMIT 15`)
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
		if state.String != "" {
			fmt.Printf("  %s: %s, %s\n", name.String, country.String, state.String)
		} else {
			fmt.Printf("  %s: %s\n", name.String, country.String)
		}
	}
	fmt.Printf("\nRestaurants still without country:\n")
	sampleRows2, err := db.Query(`SELECT name, address, location, country, us_state FROM restaurants WHERE country = '' LIMIT 10`)
	if err != nil {
		fmt.Printf("Error querying sample results: %v\n", err)
		return
	}
	defer sampleRows2.Close()
	count := 0
	for sampleRows2.Next() {
		var name, address, location, country, state sql.NullString
		err := sampleRows2.Scan(&name, &address, &location, &country, &state)
		if err != nil {
			continue
		}
		fmt.Printf("  %s: %s | %s\n", name.String, address.String, location.String)
		count++
	}
	if count == 0 {
		fmt.Printf("  All restaurants now have country information!\n")
	}
}
