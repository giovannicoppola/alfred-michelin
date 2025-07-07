package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func inferColumnTypes(rows [][]string) []string {
	columnTypes := make([]string, len(rows[0]))

	for col := range rows[0] {
		isInt, isFloat := true, true
		for row := 0; row < len(rows) && row < 10; row++ {
			val := rows[row][col]
			if _, err := strconv.Atoi(val); err != nil {
				isInt = false
			}
			if _, err := strconv.ParseFloat(val, 64); err != nil {
				isFloat = false
			}
		}
		if isInt {
			columnTypes[col] = "INTEGER"
		} else if isFloat {
			columnTypes[col] = "REAL"
		} else {
			columnTypes[col] = "TEXT"
		}
	}
	return columnTypes
}

func createTableFromCSV(db *sql.DB, csvFile string) error {
	file, err := os.Open(csvFile)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		return err
	}

	if len(rows) < 2 {
		return fmt.Errorf("CSV file must have at least a header and one row of data")
	}

	headers := rows[0]
	columnTypes := inferColumnTypes(rows[1:])

	columns := []string{"id INTEGER PRIMARY KEY AUTOINCREMENT"}
	for i, header := range headers {
		header = strings.ReplaceAll(header, " ", "_") // Ensure valid SQL column names
		columns = append(columns, fmt.Sprintf("%s %s", header, columnTypes[i]))
	}

	tableName := "csv_data"
	schema := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", tableName, strings.Join(columns, ", "))
	_, err = db.Exec(schema)
	if err != nil {
		return err
	}

	// Insert data
	placeholders := make([]string, len(headers))
	for i := range placeholders {
		placeholders[i] = "?"
	}

	insertStmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", tableName, strings.Join(headers, ","), strings.Join(placeholders, ","))
	stmt, err := db.Prepare(insertStmt)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, row := range rows[1:] {
		values := make([]interface{}, len(row))
		for i, v := range row {
			switch columnTypes[i] {
			case "INTEGER":
				if intValue, err := strconv.Atoi(v); err == nil {
					values[i] = intValue
				} else {
					values[i] = v
				}
			case "REAL":
				if floatValue, err := strconv.ParseFloat(v, 64); err == nil {
					values[i] = floatValue
				} else {
					values[i] = v
				}
			default:
				values[i] = v
			}
		}
		_, err := stmt.Exec(values...)
		if err != nil {
			return err
		}
	}

	return nil
}

func main() {
	db, err := sql.Open("sqlite3", "csv_data.db")
	if err != nil {
		fmt.Println("Error opening database:", err)
		return
	}
	defer db.Close()

	csvFile := "michelin_my_maps.csv" // Change this to your CSV file path
	if err := createTableFromCSV(db, csvFile); err != nil {
		fmt.Println("Error processing CSV:", err)
	}
}
