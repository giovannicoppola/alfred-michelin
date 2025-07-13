# Images Batch Processing

The `images-batch` command processes all restaurants in your database to scrape their image URLs and generate a comprehensive report.

## Features

- **Batch Processing**: Processes all restaurants with Michelin URLs in the database
- **Smart Skipping**: Skips restaurants that already have image URLs (unless marked as "failed")
- **Failure Tracking**: Marks failed attempts with "failed" so you can retry later
- **Comprehensive Report**: Generates a detailed markdown report with statistics
- **Respectful Scraping**: Includes delays between requests to avoid overwhelming the server

## Usage

### Basic Usage

```bash
./mym images-batch
```

This will:
- Use the default database at `data/michelin.db`
- Process all restaurants with URLs
- Save the report to `image_scraping_report.md`

### Test with Limited Restaurants

```bash
./mym images-batch -limit 10
```

This will:
- Process only the first 10 restaurants
- Perfect for testing the functionality

### Retry Failed Restaurants

```bash
./mym images-batch -retry-failed
```

This will:
- Reprocess restaurants that were previously marked as "failed"
- Useful for retrying after fixing issues or when the site is more responsive

### Custom Database Path

```bash
./mym images-batch -db /path/to/your/database.db
```

### Custom Report Path

```bash
./mym images-batch -report my_custom_report.md
```

### Debug Logging

```bash
./mym images-batch -log debug
```

### Complete Example

```bash
./mym images-batch -db data/michelin.db -report reports/image_scraping_$(date +%Y%m%d).md -log info
```

### Testing Examples

```bash
# Test with just 5 restaurants
./mym images-batch -limit 5 -log debug

# Retry only failed restaurants
./mym images-batch -retry-failed -limit 20

# Process first 50 restaurants, retry failed ones
./mym images-batch -limit 50 -retry-failed
```

## What It Does

1. **Reads Database**: Gets all restaurants with Michelin URLs from the database
2. **Scrapes Images**: For each restaurant, attempts to extract the image URL from the Michelin page
3. **Updates Database**: Saves the image URL (or "failed") to the `ImageURL` field
4. **Generates Report**: Creates a detailed markdown report with:
   - Summary statistics (total, successful, failed, success rate)
   - Timing information (start, end, duration)
   - List of successful scrapes with image URLs
   - List of failed scrapes with error messages

## Report Format

The generated report includes:

```markdown
# Restaurant Image Scraping Report

## Summary
- **Total Restaurants Processed**: 150
- **Successful**: 142
- **Failed**: 8
- **Success Rate**: 94.67%
- **Start Time**: 2024-01-15 10:30:00
- **End Time**: 2024-01-15 11:45:00
- **Duration**: 1h15m0s

## Detailed Results

### Successful Scrapes (142)
- **Osteria Mozza** (ID: 123) - [https://axwwgrkdco.cloudimg.io/...](https://guide.michelin.com/...)
- **Providence** (ID: 124) - [https://axwwgrkdco.cloudimg.io/...](https://guide.michelin.com/...)

### Failed Scrapes (8)
- **Restaurant Name** (ID: 125) - [https://guide.michelin.com/...](https://guide.michelin.com/...) - Error: no image found with the specified URL prefix
```

## Database Schema

The command updates the `ImageURL` field in the `Restaurant` table:

- **Successful**: Contains the actual image URL
- **Failed**: Contains "failed" so you can identify and retry later
- **Already Processed**: Skips restaurants that already have an image URL (unless it's "failed")

## Error Handling

- **Network Errors**: Logged and marked as "failed"
- **Missing Images**: Logged and marked as "failed"
- **Database Errors**: Logged but processing continues
- **Individual Failures**: Don't stop the batch process

## Performance

- **Rate Limiting**: 2-second delay between requests
- **Progress Logging**: Shows current restaurant being processed
- **Resume Capability**: Can be run multiple times (skips already processed restaurants)

## Example Output

```
INFO[0000] starting images-batch command
INFO[0000] Starting batch image processing                  count=150
INFO[0000] Processing restaurant                            restaurant_id=123 restaurant_name="Osteria Mozza" restaurant_url="https://guide.michelin.com/en/california/us-los-angeles/restaurant/osteria-mozza" progress="1/150"
INFO[0002] Processing restaurant                            restaurant_id=124 restaurant_name="Providence" restaurant_url="https://guide.michelin.com/en/california/us-los-angeles/restaurant/providence" progress="2/150"
...
INFO[0450] Batch processing completed                       total=150 successful=142 failed=8 success_rate="94.67%" duration=7m30s
INFO[0450] Report saved successfully                        report_path=image_scraping_report.md
``` 