package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Restaurant struct {
	Name     string
	Location string
	URL      string
	Cuisine  string
	Award    string
	Price    string
	Address  string
}

// Helper function to get string from sql.NullString
func getString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// saveMissingRestaurants saves restaurants to a CSV file
func saveMissingRestaurants(filePath string, restaurants []Restaurant, description string) error {
	outputFile, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	writer := csv.NewWriter(outputFile)
	defer writer.Flush()

	// Write header
	header := []string{"Name", "Location", "URL", "Cuisine", "Award", "Price", "Address"}
	if err := writer.Write(header); err != nil {
		return err
	}

	// Write restaurants
	for _, restaurant := range restaurants {
		record := []string{
			restaurant.Name,
			restaurant.Location,
			restaurant.URL,
			restaurant.Cuisine,
			restaurant.Award,
			restaurant.Price,
			restaurant.Address,
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}

// displayRestaurantSummary displays a formatted summary of restaurants
func displayRestaurantSummary(restaurants []Restaurant, filePath string) {
	fmt.Printf("%-60s | %-40s | %s\n", "Name", "Location", "Award")
	fmt.Printf("%s\n", string(make([]rune, 120)))

	displayCount := 10
	if len(restaurants) < displayCount {
		displayCount = len(restaurants)
	}

	for i := 0; i < displayCount; i++ {
		restaurant := restaurants[i]
		name := restaurant.Name
		location := restaurant.Location
		award := restaurant.Award

		// Truncate for display
		if len(name) > 60 {
			name = name[:57] + "..."
		}
		if len(location) > 40 {
			location = location[:37] + "..."
		}

		fmt.Printf("%-60s | %-40s | %s\n", name, location, award)
	}

	if len(restaurants) > displayCount {
		fmt.Printf("... and %d more (see %s for complete list)\n", len(restaurants)-displayCount, filePath)
	}
}

// generateMarkdownReport creates a comprehensive markdown report of the analysis
func generateMarkdownReport(filePath string, dbCount, csvCount int, missingFromDb, missingFromCsv []Restaurant, dbPath, csvPath, missingFromDbPath, missingFromCsvPath, reportPath string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Get current timestamp
	timestamp := fmt.Sprintf("%s", time.Now().Format("2006-01-02 15:04:05"))

	// Write markdown content
	content := fmt.Sprintf(`# Michelin Database vs CSV Comparison Report

**Generated:** %s

## 📊 Summary Statistics

| Metric | Count |
|--------|-------|
| Restaurants in Database | %d |
| Restaurants in CSV | %d |
| Missing from Database | %d |
| Missing from CSV | %d |
| **Total Discrepancies** | **%d** |

## 📂 Source Files

- **Database:** %s
- **CSV Export:** %s

## 🔍 Analysis Results

`, timestamp, dbCount, csvCount, len(missingFromDb), len(missingFromCsv), len(missingFromDb)+len(missingFromCsv), dbPath, csvPath)

	// Add missing from database section
	if len(missingFromDb) > 0 {
		content += fmt.Sprintf(`### 🆕 Restaurants Missing from Database (%d)

*These restaurants exist in the CSV export but not in your database. They are likely new additions to the Michelin Guide that should be imported.*

| Name | Location | Award | Action |
|------|----------|--------|--------|
`, len(missingFromDb))

		displayCount := 20 // Show more in the report
		if len(missingFromDb) < displayCount {
			displayCount = len(missingFromDb)
		}

		for i := 0; i < displayCount; i++ {
			r := missingFromDb[i]
			content += fmt.Sprintf("| %s | %s | %s | ➕ Add to DB |\n", r.Name, r.Location, r.Award)
		}

		if len(missingFromDb) > displayCount {
			content += fmt.Sprintf("\n*... and %d more restaurants. See `%s` for the complete list.*\n", len(missingFromDb)-displayCount, missingFromDbPath)
		}

		content += "\n**Recommendation:** Import these restaurants to keep your database current with the latest Michelin Guide.\n\n"
	}

	// Add missing from CSV section
	if len(missingFromCsv) > 0 {
		content += fmt.Sprintf(`### 🗑️ Restaurants Missing from CSV (%d)

*These restaurants exist in your database but not in the CSV export. They may have been removed from the Michelin Guide or lost their status.*

| Name | Location | Award | Action |
|------|----------|--------|--------|
`, len(missingFromCsv))

		displayCount := 20
		if len(missingFromCsv) < displayCount {
			displayCount = len(missingFromCsv)
		}

		for i := 0; i < displayCount; i++ {
			r := missingFromCsv[i]
			content += fmt.Sprintf("| %s | %s | %s | ⚠️ Review |\n", r.Name, r.Location, r.Award)
		}

		if len(missingFromCsv) > displayCount {
			content += fmt.Sprintf("\n*... and %d more restaurants. See `%s` for the complete list.*\n", len(missingFromCsv)-displayCount, missingFromCsvPath)
		}

		content += "\n**Recommendation:** Review these restaurants to determine if they should be archived or removed from your database.\n\n"
	}

	// Add conclusions
	if len(missingFromDb) == 0 && len(missingFromCsv) == 0 {
		content += `## ✅ Conclusion

**Perfect Synchronization!** Your database and the CSV export are perfectly in sync. No action required.

`
	} else {
		content += `## 📋 Action Items

`
		if len(missingFromDb) > 0 {
			content += fmt.Sprintf("1. **Import %d new restaurants** from `%s`\n", len(missingFromDb), missingFromDbPath)
		}
		if len(missingFromCsv) > 0 {
			content += fmt.Sprintf("2. **Review %d potentially delisted restaurants** from `%s`\n", len(missingFromCsv), missingFromCsvPath)
		}
		content += "\n"
	}

	// Add file information
	content += fmt.Sprintf(`## 📁 Output Files

- **%s** - Restaurants to import into database
- **%s** - Restaurants to review for potential removal
- **%s** - This analysis report

---

*Generated by Alfred Michelin Database Comparison Tool*
`, missingFromDbPath, missingFromCsvPath, reportPath)

	_, err = file.WriteString(content)
	return err
}

func main() {
	// Generate timestamp for file naming
	timestamp := time.Now().Format("2006-01-02_15-04-05")

	// Paths
	dbPath := "/Users/giovanni/Library/Application Support/Alfred/Workflow Data/com.giovanni.alfred-michelin/michelin new.db"
	csvPath := "/Users/giovanni/gDrive/GitHub repos/alfred-michelin/database builder/michelin-my-maps-3.2.1/data/michelin_my_maps.csv"
	missingFromDbPath := fmt.Sprintf("missing_from_database_%s.csv", timestamp)
	missingFromCsvPath := fmt.Sprintf("missing_from_csv_%s.csv", timestamp)
	reportPath := fmt.Sprintf("comparison_report_%s.md", timestamp)

	fmt.Println("🔍 Comparing CSV file with database...")
	fmt.Printf("Database: %s\n", dbPath)
	fmt.Printf("CSV file: %s\n", csvPath)
	fmt.Println()

	// Read restaurants from database (with full details for missing comparison)
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		fmt.Printf("❌ Error opening database: %v\n", err)
		return
	}
	defer db.Close()

	dbURLs := make(map[string]bool)
	dbRestaurants := make(map[string]Restaurant)

	query := `SELECT r.name, r.location, r.url, r.cuisine, ra.distinction, r.address 
	          FROM restaurants r
	          LEFT JOIN (
				SELECT 
					ra1.restaurant_id, 
					ra1.distinction
				FROM restaurant_awards ra1
				WHERE ra1.distinction = (
					SELECT ra2.distinction 
					FROM restaurant_awards ra2 
					WHERE ra2.restaurant_id = ra1.restaurant_id 
					ORDER BY ra2.year DESC 
					LIMIT 1
				)
			) ra ON r.id = ra.restaurant_id
	          WHERE r.url IS NOT NULL AND r.url != ''`
	rows, err := db.Query(query)
	if err != nil {
		fmt.Printf("❌ Error querying database: %v\n", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var name, location, url, cuisine, award, address sql.NullString
		if err := rows.Scan(&name, &location, &url, &cuisine, &award, &address); err != nil {
			fmt.Printf("⚠️  Error scanning row: %v\n", err)
			continue
		}

		if url.Valid {
			dbURLs[url.String] = true
			dbRestaurants[url.String] = Restaurant{
				Name:     getString(name),
				Location: getString(location),
				URL:      url.String,
				Cuisine:  getString(cuisine),
				Award:    getString(award),
				Address:  getString(address),
				Price:    "", // Not available in database
			}
		}
	}

	fmt.Printf("📊 Found %d restaurants in database\n", len(dbURLs))

	// Read restaurants from CSV
	csvFile, err := os.Open(csvPath)
	if err != nil {
		fmt.Printf("❌ Error opening CSV: %v\n", err)
		return
	}
	defer csvFile.Close()

	reader := csv.NewReader(csvFile)
	records, err := reader.ReadAll()
	if err != nil {
		fmt.Printf("❌ Error reading CSV: %v\n", err)
		return
	}

	if len(records) == 0 {
		fmt.Printf("❌ No records found in CSV\n")
		return
	}

	fmt.Printf("📊 Found %d restaurants in CSV\n", len(records)-1) // -1 for header

	// Parse header to find column indices
	header := records[0]
	columnIndex := make(map[string]int)
	for i, col := range header {
		columnIndex[col] = i
	}

	// Find restaurants missing from database (in CSV but not in DB)
	var missingFromDb []Restaurant
	csvURLs := make(map[string]bool)

	for i := 1; i < len(records); i++ { // Skip header
		record := records[i]

		// Get URL from the record
		urlIdx, exists := columnIndex["Url"]
		if !exists || urlIdx >= len(record) {
			continue
		}
		url := record[urlIdx]
		if url == "" {
			continue
		}

		csvURLs[url] = true

		// Check if this URL exists in database
		if !dbURLs[url] {
			// Safely get column values
			var name, location, cuisine, award, price, address string

			if idx, exists := columnIndex["Name"]; exists && idx < len(record) {
				name = record[idx]
			}
			if idx, exists := columnIndex["Location"]; exists && idx < len(record) {
				location = record[idx]
			}
			if idx, exists := columnIndex["Cuisine"]; exists && idx < len(record) {
				cuisine = record[idx]
			}
			if idx, exists := columnIndex["Award"]; exists && idx < len(record) {
				award = record[idx]
			}
			if idx, exists := columnIndex["Price"]; exists && idx < len(record) {
				price = record[idx]
			}
			if idx, exists := columnIndex["Address"]; exists && idx < len(record) {
				address = record[idx]
			}

			restaurant := Restaurant{
				Name:     name,
				Location: location,
				URL:      url,
				Cuisine:  cuisine,
				Award:    award,
				Price:    price,
				Address:  address,
			}
			missingFromDb = append(missingFromDb, restaurant)
		}
	}

	// Find restaurants missing from CSV (in DB but not in CSV)
	var missingFromCsv []Restaurant
	for url, restaurant := range dbRestaurants {
		if !csvURLs[url] {
			missingFromCsv = append(missingFromCsv, restaurant)
		}
	}

	fmt.Printf("📊 Total unique URLs in CSV: %d\n", len(csvURLs))
	fmt.Printf("🔎 Found %d restaurants missing from database\n", len(missingFromDb))
	fmt.Printf("🔎 Found %d restaurants missing from CSV\n", len(missingFromCsv))
	fmt.Println()

	// Save missing from database restaurants
	if len(missingFromDb) > 0 {
		if err := saveMissingRestaurants(missingFromDbPath, missingFromDb, "Missing from Database"); err != nil {
			fmt.Printf("❌ Error saving missing from database: %v\n", err)
		} else {
			fmt.Printf("💾 Saved restaurants missing from database to: %s\n", missingFromDbPath)
		}
	}

	// Save missing from CSV restaurants
	if len(missingFromCsv) > 0 {
		if err := saveMissingRestaurants(missingFromCsvPath, missingFromCsv, "Missing from CSV"); err != nil {
			fmt.Printf("❌ Error saving missing from CSV: %v\n", err)
		} else {
			fmt.Printf("💾 Saved restaurants missing from CSV to: %s\n", missingFromCsvPath)
		}
	}
	fmt.Println()

	// Display summaries
	if len(missingFromDb) > 0 {
		fmt.Println("📋 Restaurants missing from database (in CSV but not in DB):")
		displayRestaurantSummary(missingFromDb, missingFromDbPath)
		fmt.Println()
	}

	if len(missingFromCsv) > 0 {
		fmt.Println("📋 Restaurants missing from CSV (in DB but not in CSV):")
		displayRestaurantSummary(missingFromCsv, missingFromCsvPath)
		fmt.Println()
	}

	// Generate markdown report
	if err := generateMarkdownReport(reportPath, len(dbURLs), len(csvURLs), missingFromDb, missingFromCsv, dbPath, csvPath, missingFromDbPath, missingFromCsvPath, reportPath); err != nil {
		fmt.Printf("❌ Error generating report: %v\n", err)
	} else {
		fmt.Printf("📄 Generated analysis report: %s\n", reportPath)
	}

	if len(missingFromDb) == 0 && len(missingFromCsv) == 0 {
		fmt.Println("✅ Perfect match! Database and CSV are in sync.")
	} else {
		fmt.Printf("✅ Analysis complete! Check output files for detailed listings.\n")
	}
}
