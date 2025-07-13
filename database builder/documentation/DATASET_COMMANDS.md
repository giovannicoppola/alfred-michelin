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

## 🔄 Database Update via Scraping

### Overview
This scenario covers updating your existing database with fresh data from the Michelin Guide website, including new restaurants, updated information, and image URLs.

### Prerequisites
- **Existing database**: `data/michelin.db` must exist
- **Backup recommended**: Copy your database before major updates
- **Internet connection**: Required for web scraping

### Step-by-Step Update Process

#### Step 1: Backup Current Database
```bash
# Create backup with timestamp
cp data/michelin.db data/michelin_backup_$(date +%Y%m%d_%H%M%S).db

# Or use a descriptive name
cp data/michelin.db data/michelin_before_update_$(date +%Y%m%d).db
```

#### Step 2: Update Restaurant Data via Scraping
```bash
# Build application (if not already built)
go build -o mym cmd/mym/mym.go

# Run scraping to update restaurant data
./mym scrape -log info

# For testing with limited restaurants first
./mym scrape -limit 10 -log debug
```

#### Step 3: Update Historical Data (Optional)
```bash
# Backfill historical data for existing restaurants
./mym backfill

# Or backfill specific restaurant
./mym backfill "https://guide.michelin.com/en/restaurant-url"
```

#### Step 4: Add Image URLs
```bash
# Test image scraping with limited restaurants
./mym images-batch -limit 10 -log debug

# Process all restaurants for images
./mym images-batch -log info

# Retry failed images if needed
./mym images-batch -retry-failed
```

#### Step 5: Fix Normalized Fields (Required for Alfred Workflow)
```bash
# Check if normalized fields need updating
sqlite3 data/michelin.db "SELECT COUNT(*) as total, COUNT(name_normalized) as normalized FROM restaurants;"

# Run normalization script to populate missing normalized fields
go run fix_normalized_fields.go

# Verify normalization completed
sqlite3 data/michelin.db "SELECT COUNT(*) as missing_normalized FROM restaurants WHERE name_normalized IS NULL OR name_normalized = '';"
```

**Why this step is needed:**
- The Alfred workflow requires `name_normalized`, `location_normalized`, and `cuisine_normalized` fields for accent-insensitive search
- New restaurants added via scraping don't automatically get these fields populated
- This step ensures all restaurants have normalized fields for proper Alfred workflow functionality

#### Step 6: Verify Updates
```bash
# View database with datasette
make datasette

# Or check database directly
sqlite3 data/michelin.db "SELECT COUNT(*) FROM restaurants;"
sqlite3 data/michelin.db "SELECT COUNT(*) FROM restaurants WHERE image_url != '';"
sqlite3 data/michelin.db "SELECT COUNT(*) FROM restaurants WHERE InGuide = 1;"
```

### Complete Update Script

Create a script `update_database.sh`:
```bash
#!/bin/bash

# Database Update Script
echo "🍽️ Starting Michelin Database Update"

# Step 1: Backup
echo "📦 Creating backup..."
cp data/michelin.db data/michelin_backup_$(date +%Y%m%d_%H%M%S).db

# Step 2: Scrape new data
echo "🌐 Scraping restaurant data..."
./mym scrape -log info

# Step 3: Add images
echo "🖼️ Adding image URLs..."
./mym images-batch -log info

# Step 4: Fix normalized fields
echo "🔤 Fixing normalized fields for Alfred workflow..."
go run fix_normalized_fields.go

# Step 5: Generate summary
echo "📊 Generating update report..."
./mym dataset -log info

echo "✅ Database update complete!"
```

Make it executable: `chmod +x update_database.sh`

### Update Verification Commands

#### Check Database Statistics
```bash
# Total restaurants
sqlite3 data/michelin.db "SELECT COUNT(*) as total_restaurants FROM restaurants;"

# Restaurants with images
sqlite3 data/michelin.db "SELECT COUNT(*) as with_images FROM restaurants WHERE image_url != '' AND image_url != 'failed';"

# Currently in guide
sqlite3 data/michelin.db "SELECT COUNT(*) as in_guide FROM restaurants WHERE InGuide = 1;"

# Recent updates (last 24 hours)
sqlite3 data/michelin.db "SELECT COUNT(*) as recent_updates FROM restaurants WHERE updated_at > datetime('now', '-1 day');"
```

#### Compare Before/After
```bash
# Before update
sqlite3 data/michelin.db "SELECT COUNT(*) FROM restaurants;" > before_count.txt

# After update
sqlite3 data/michelin.db "SELECT COUNT(*) FROM restaurants;" > after_count.txt

# Compare
echo "Restaurant count change:"
diff before_count.txt after_count.txt
```

### Expected Update Reports

#### Scraping Report
- Console output shows restaurants processed
- New restaurants added to database
- Existing restaurants updated

#### Image Scraping Report
- Generated file: `image_scraping_report_YYYYMMDD_HHMMSS.md`
- Shows successful/failed image extractions
- Includes processing statistics and timing

#### Dataset Processing Report
- Generated file: `dataset_processing_report_YYYY-MM-DD_HH-MM-SS.md`
- Shows CSV processing results
- Includes InGuide logic application

### Troubleshooting Updates

#### Common Issues
1. **No new restaurants found**
   - Check if scraping completed successfully
   - Verify website structure hasn't changed
   - Run with `-log debug` for detailed output

2. **All images failed**
   - Check internet connection
   - Verify Michelin website accessibility
   - Try with `-limit 5` to test

3. **Database locked**
   - Ensure no other processes are using the database
   - Close any open database viewers
   - Restart the process

4. **Alfred workflow search not working properly**
   - Check if normalized fields are populated: `sqlite3 data/michelin.db "SELECT COUNT(*) FROM restaurants WHERE name_normalized IS NULL OR name_normalized = '';"`
   - Run normalization script: `go run fix_normalized_fields.go`
   - Verify Alfred workflow can find restaurants with accented characters

#### Recovery Steps
```bash
# If scraping fails, restore backup
cp data/michelin_backup_YYYYMMDD_HHMMSS.db data/michelin.db

# If images fail, retry just images
./mym images-batch -retry-failed

# If partial update, check what was processed
sqlite3 data/michelin.db "SELECT COUNT(*) FROM restaurants WHERE updated_at > datetime('now', '-1 hour');"
```

### Update Frequency Recommendations

#### Daily Updates
- **Scraping**: Not recommended (rate limiting)
- **Images**: Only for new restaurants
- **Historical data**: Only for specific restaurants

#### Weekly Updates
- **Full scraping**: Recommended for keeping data current
- **Image updates**: Process any new restaurants
- **Data validation**: Check for anomalies

#### Monthly Updates
- **Complete refresh**: Full scrape + images + historical
- **Database maintenance**: Clean up old data
- **Backup rotation**: Archive old backups

---
**💡 TIP**: Always run with `-log debug` to see exactly what's happening! 