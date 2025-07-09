# Tools Directory

This directory contains utility scripts for maintaining the Alfred Michelin Workflow.

## compare_csv_database.go

A Go script that performs a bidirectional comparison between the latest CSV export and the database to identify discrepancies.

### Usage

```bash
go run compare_csv_database.go
```

### What it does

1. Reads restaurant data from both the SQLite database and CSV export file
2. **Identifies restaurants missing from database** (exist in CSV but not in DB)
3. **Identifies restaurants missing from CSV** (exist in DB but not in CSV)
4. Saves both sets of missing restaurants to separate CSV files
5. **Generates a comprehensive markdown report** with analysis and recommendations
6. Displays summaries of both types of discrepancies

### File Paths

The script uses these hardcoded paths:
- Database: `/Users/giovanni/Library/Application Support/Alfred/Workflow Data/com.giovanni.alfred-michelin/michelin new.db`
- CSV: `/Users/giovanni/gDrive/GitHub repos/alfred-michelin/database builder/michelin-my-maps-3.2.1/data/michelin_my_maps.csv`
- Output files (timestamped to preserve history):
  - `missing_from_database_YYYY-MM-DD_HH-MM-SS.csv` (restaurants in CSV but not in DB)
  - `missing_from_csv_YYYY-MM-DD_HH-MM-SS.csv` (restaurants in DB but not in CSV)
  - `comparison_report_YYYY-MM-DD_HH-MM-SS.md` (comprehensive analysis report)

### Output

- **Console output** showing detailed comparison statistics
- **CSV files** with complete restaurant details for each discrepancy type:
  - `missing_from_database.csv` (restaurants to import)
  - `missing_from_csv.csv` (restaurants to review)
- **Markdown report** (`comparison_report.md`) with formatted analysis and recommendations
- **Summary tables** showing sample restaurants from each category

### Use Cases

- **Database Updates**: Identify new restaurants that should be added to the database
- **Data Validation**: Find restaurants that may have been removed from the Michelin guide
- **Quality Assurance**: Ensure database completeness and accuracy
- **Regular Maintenance**: Monitor changes in the Michelin restaurant listings
- **Historical Tracking**: Timestamped files preserve comparison history over time

### Dependencies

Requires the SQLite3 driver for Go:
```bash
go mod tidy
``` 