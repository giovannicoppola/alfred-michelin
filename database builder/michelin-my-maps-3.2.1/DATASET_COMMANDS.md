# 🍽️ Michelin Dataset Commands - Quick Reference

## 🚀 Most Common Use Case: Update Database with New CSV

### Step 1: Prepare CSV File
- Place your new CSV file in: `data/HistoricalData/`
- Ensure filename contains date (e.g., `2025-03-15_michelin_guide.csv`)
- CSV format: `Name,Address,Location,Price,Type,Longitude,Latitude,PhoneNumber,Url,WebsiteUrl,Classification`

### Step 2: Run Processing Command
```bash
# Build application
go build -o mym cmd/mym/mym.go

# Process all CSV files (THIS IS THE MAIN COMMAND YOU NEED)
./mym dataset -log debug
```

## 🧠 What Happens (InGuide Logic)

### Recent Files (≤1 month old)
- ✅ New restaurants → `InGuide=true` (currently in guide)
- ✅ Existing restaurants in CSV → Stay `InGuide=true`, add awards  
- ⚠️ Existing restaurants missing from CSV → `InGuide=false` (removed from guide)

### Historical Files (>1 month old)  
- 📜 New restaurants → `InGuide=false` (historical only)
- 🔒 Existing restaurants → `InGuide` status preserved, only add awards

## 🔧 Other Useful Commands

```bash
# Test with small subset first
./mym dataset -limit 5 -log debug

# Silent processing (less verbose)
./mym dataset -log info

# Just process, no detailed logs
./mym dataset
```

## 📊 After Processing

- Check console for errors
- Review generated report: `data/dataset_processing_report_YYYY-MM-DD_HH-MM-SS.md`
- Database updated: `data/michelin.db`

## 🆘 If Something Goes Wrong

1. **Check the markdown report** for detailed statistics
2. **Test with `-limit 5`** first to verify logic
3. **Backup your database** before major imports
4. **Check CSV format** matches expected columns

---
**💡 TIP**: Always run with `-log debug` to see exactly what's happening! 