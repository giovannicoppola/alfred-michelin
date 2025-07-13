# Image Scraping Functionality - Technical Documentation

## Overview

The Michelin My Maps scraper now includes comprehensive image scraping functionality that extracts restaurant images from Michelin Guide pages and stores the image URLs in the database. This feature supports both individual restaurant processing and batch processing of entire databases.

## Architecture

### System Components

```
internal/
├── imagescraper/
│   ├── scraper.go      # Core image scraping logic
│   ├── batch.go        # Batch processing functionality
│   └── utils.go        # File utility functions
cmd/
└── mym/
    └── mym.go          # CLI commands: images, images-batch
```

### Database Schema Updates

The `Restaurant` model has been extended with an `ImageURL` field:

```go
type Restaurant struct {
    // ... existing fields ...
    ImageURL string // URL to the restaurant's main image
    // ... existing fields ...
}
```

## Image Scraping Commands

### 1. Single Restaurant Image Scraping (`images`)

#### Basic Usage
```bash
./mym images -url "https://guide.michelin.com/en/california/us-los-angeles/restaurant/osteria-mozza"
```

#### Options
- `-url <url>` - Single restaurant URL to scrape image for
- `-file <path>` - File containing restaurant URLs (one per line)
- `-url-only` - Only extract image URL, don't require restaurant in database
- `-log <level>` - Set log level (debug, info, warning, error, fatal, panic)

#### Examples
```bash
# Extract image URL for a single restaurant
./mym images -url "https://guide.michelin.com/en/california/us-los-angeles/restaurant/osteria-mozza" -url-only

# Process multiple restaurants from a file
./mym images -file restaurants.txt

# With debug logging
./mym images -file restaurants.txt -log debug
```

### 2. Batch Image Processing (`images-batch`)

#### Basic Usage
```bash
./mym images-batch
```

#### Options
- `-db <path>` - Database path (default: data/michelin.db)
- `-limit <number>` - Maximum number of restaurants to process (0 = no limit)
- `-retry-failed` - Retry restaurants that were previously marked as failed
- `-report <path>` - Output report file path (default: timestamped filename)
- `-log <level>` - Set log level

#### Examples
```bash
# Test with limited restaurants
./mym images-batch -limit 10

# Process all restaurants in database
./mym images-batch

# Retry failed restaurants
./mym images-batch -retry-failed

# Custom database and report path
./mym images-batch -db /path/to/database.db -report my_report.md

# Process with debug logging
./mym images-batch -limit 50 -log debug
```

## Technical Implementation

### Image Detection Algorithm

The scraper uses a multi-layered approach to find restaurant images:

1. **Meta Tags (Primary)**
   ```html
   <meta property="og:image" content="https://axwwgrkdco.cloudimg.io/...">
   ```

2. **Data Attributes (Secondary)**
   ```html
   <div data-ci-bg-url="https://axwwgrkdco.cloudimg.io/...">
   ```

3. **Image Tags (Fallback)**
   ```html
   <img src="https://axwwgrkdco.cloudimg.io/...">
   ```

### URL Filtering
- Only images from Michelin CDN (`axwwgrkdco.cloudimg.io`) are considered
- Query parameters are stripped for consistency
- Duplicate URLs are avoided

### HTTP Request Configuration
```go
req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36...")
req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
req.Header.Set("Accept-Language", "en-US,en;q=0.5")
req.Header.Set("Connection", "keep-alive")
req.Header.Set("Upgrade-Insecure-Requests", "1")
```

### Gzip Response Handling
The scraper automatically handles gzip-encoded responses:
```go
switch resp.Header.Get("Content-Encoding") {
case "gzip":
    reader, err = gzip.NewReader(resp.Body)
    // ... handle decompression
default:
    reader = resp.Body
}
```

## Batch Processing Features

### Smart Skipping Logic
- **Already Processed**: Restaurants with valid image URLs are skipped
- **Previously Failed**: Restaurants marked as "failed" are skipped (unless `-retry-failed`)
- **Instant Processing**: Skipped restaurants are processed instantly (no delays)

### Processing Order
- **Most Recent First**: Restaurants are processed in descending order by `created_at` timestamp
- **Newest Priority**: Most recently added restaurants are processed first
- **Chronological Processing**: Ensures the latest data gets priority attention

### Limit Processing
- **Limit Applies to Actual Processing**: Only counts restaurants that require scraping
- **Skips Don't Count**: Already processed restaurants don't count toward the limit
- **Example**: `-limit 10` might check 50 restaurants but only scrape 10 new ones

### Rate Limiting
- **2-second delays** between actual scraping operations
- **No delays** for skipped restaurants
- **Respectful scraping** to avoid overwhelming the server

### Error Handling
- **Individual failures** don't stop the batch process
- **Failed restaurants** are marked with "failed" in the database
- **Comprehensive logging** of all operations

## Report Generation

### Report Structure
```markdown
# Restaurant Image Scraping Report

## Summary
- **Total Restaurants in Database**: 1000
- **Processed (Downloaded + Skipped)**: 150 (15.0% of total)
- **Successful**: 8
- **Failed**: 2
- **Skipped**: 140
- **Success Rate**: 80.00%
- **Start Time (EDT)**: 2024-01-15 14:30:22
- **End Time (EDT)**: 2024-01-15 14:35:52
- **Duration**: 5m 30s

## Detailed Results

### Successful Scrapes (8)
- **Restaurant Name** (ID: 123) - [Image URL](Restaurant URL)

### Failed Scrapes (2)
- **Restaurant Name** (ID: 124) - [Restaurant URL](Restaurant URL) - Error: description
```

### Report Features
- **EDT Timestamps**: All timestamps in Eastern Daylight Time
- **Processed Fraction**: Shows what percentage of database was processed
- **Clean Duration**: Formatted as "Xm Ys" for readability
- **Skipped Count**: Tracks skipped restaurants without listing them in details
- **Timestamped Filenames**: Automatic filename generation with EDT timestamps

## Database Operations

### Image URL Storage
- **Successful scrapes**: Store actual image URL
- **Failed scrapes**: Store "failed" marker
- **Skipped restaurants**: Preserve existing ImageURL value

### Database Updates
```go
// Update restaurant with image URL
restaurant.ImageURL = imageURL
if err := repo.SaveRestaurant(ctx, &restaurant); err != nil {
    // Handle error
}
```

### Query Operations
```go
// Get all restaurants with URLs for processing
restaurants, err := repo.ListAllRestaurantsWithURL()

// Find specific restaurant by URL
restaurant, err := repo.FindRestaurantByURL(ctx, url)
```

## Performance Considerations

### Storage Strategy
- **URL-only storage**: Stores image URLs, not actual image files
- **On-demand loading**: Images can be loaded when needed
- **Efficient storage**: ~18,000 restaurants with URLs vs. actual image files

### Processing Efficiency
- **Smart skipping**: Avoids reprocessing already handled restaurants
- **Batch operations**: Processes multiple restaurants efficiently
- **Resume capability**: Can be interrupted and resumed

### Memory Usage
- **Streaming processing**: Processes restaurants one at a time
- **Minimal memory footprint**: Doesn't load entire database into memory
- **Garbage collection friendly**: Releases resources after each restaurant

## Error Scenarios and Handling

### Common Error Types
1. **Network Errors**: Connection timeouts, DNS failures
2. **HTTP Errors**: 404, 403, 500 status codes
3. **Parsing Errors**: Malformed HTML, missing image elements
4. **Database Errors**: Connection issues, constraint violations

### Error Recovery
- **Individual failures**: Logged but don't stop processing
- **Retry mechanism**: `-retry-failed` flag for retrying failed restaurants
- **Partial success**: Continues processing even if some restaurants fail

### Logging Levels
- **Info**: Normal operations, progress updates
- **Debug**: Detailed processing information
- **Warning**: Non-critical issues
- **Error**: Processing failures

## Usage Patterns

### Development and Testing
```bash
# Test with small batch
./mym images-batch -limit 5 -log debug

# Test single restaurant
./mym images -url "restaurant_url" -url-only
```

### Production Processing
```bash
# Process entire database
./mym images-batch -log info

# Process in chunks
./mym images-batch -limit 100
./mym images-batch -limit 100  # Continues from where it left off
```

### Maintenance and Recovery
```bash
# Retry failed restaurants
./mym images-batch -retry-failed

# Process with custom database
./mym images-batch -db backup_database.db
```

## Integration with Existing Workflow

### Database Compatibility
- **Backward compatible**: Works with existing databases
- **Auto-migration**: Adds ImageURL field automatically
- **Data preservation**: Existing data is not affected

### Command Integration
- **Unified CLI**: All commands accessible through `./mym`
- **Consistent logging**: Uses same logging framework as other commands
- **Shared configuration**: Uses same database and configuration patterns

### Workflow Integration
1. **Scrape restaurants**: `./mym scrape`
2. **Process historical data**: `./mym dataset`
3. **Scrape images**: `./mym images-batch`
4. **Export data**: Generate reports and exports

## Future Enhancements

### Potential Features
- **Image download**: Option to download actual image files
- **Image validation**: Verify image URLs are still accessible
- **Batch size limits**: Configurable batch processing limits
- **Resume functionality**: Save progress for interrupted sessions
- **Image caching**: Local caching of frequently accessed images

### Performance Optimizations
- **Parallel processing**: Multiple restaurants simultaneously
- **Connection pooling**: Reuse HTTP connections
- **Smart retries**: Exponential backoff for failed requests
- **Progress persistence**: Save progress to resume later

## Troubleshooting

### Common Issues
1. **No restaurants found**: Check database path and content
2. **All restaurants skipped**: Verify ImageURL field values
3. **Network timeouts**: Check internet connection and rate limiting
4. **Permission errors**: Verify file write permissions for reports

### Debug Commands
```bash
# Check database content
sqlite3 data/michelin.db "SELECT COUNT(*) FROM restaurants WHERE url != '';"

# Check image URLs
sqlite3 data/michelin.db "SELECT COUNT(*) FROM restaurants WHERE image_url != '';"

# Debug with verbose logging
./mym images-batch -limit 1 -log debug
```

This documentation provides a comprehensive overview of the image scraping functionality, including technical details, usage patterns, and troubleshooting guidance. 