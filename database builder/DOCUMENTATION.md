# Michelin My Maps Scraper - Technical Documentation

## Overview

The Michelin My Maps scraper is a Go-based web scraping application that extracts restaurant data from the Michelin Guide website and stores it in a structured SQLite database. This application is designed to collect comprehensive restaurant information including location coordinates, pricing, cuisine types, and award classifications. It supports both current data scraping and historical data backfill using the Wayback Machine.

## Architecture Overview

### System Components

```
cmd/
└── mym/
    └── mym.go          # Main CLI entry point with subcommands
internal/
├── backfill/
│   ├── backfill.go     # Wayback Machine historical data scraping
│   ├── extract.go      # Data extraction from archived pages
│   └── extract_test.go # Extraction tests
├── models/
│   ├── restaurant.go   # Restaurant data model with GORM
│   └── award.go        # Award data model and URL mappings
├── parser/
│   ├── parser.go       # Text parsing and data transformation
│   └── parser_test.go  # Parser tests
├── scraper/
│   ├── scraper.go      # Main web scraping logic
│   ├── extract.go      # Data extraction from current pages
│   ├── xpath.go        # XPath selector definitions
│   └── testdata/       # Test data and utilities
├── storage/
│   ├── repository.go   # Database interface definitions
│   └── sqlite.go       # SQLite implementation with GORM
└── webclient/
    └── webclient.go    # HTTP client and Colly configuration
data/
├── michelin.db         # SQLite database (primary output)
└── michelin_my_maps.csv # CSV export (generated from database)
```

### Technology Stack

- **Go 1.24+** - Primary programming language
- **Colly v2** - Web scraping framework with caching
- **GORM** - ORM for database operations
- **SQLite** - Primary data storage
- **XPath** - HTML element selection
- **Logrus** - Structured logging
- **Phone Numbers** - Phone number parsing and formatting

## Detailed Workflow

### Phase 1: Application Initialization

#### Entry Point (`cmd/mym/mym.go`)
```go
func main() {
    // Supports subcommands: scrape, backfill
    if err := run(); err != nil {
        log.Fatalf("Error: %v", err)
    }
}
```

The application supports two main subcommands:
- `scrape` - Collects current restaurant data from Michelin Guide
- `backfill` - Retrieves historical data from Wayback Machine

#### Scrape Command Initialization

1. **Database Setup**
   - Creates SQLite database at `data/michelin.db`
   - Auto-migrates Restaurant and RestaurantAward models
   - Configures GORM with WAL mode for better performance

2. **Web Scraper Configuration**
   - Creates two Colly collectors:
     - Primary collector for listing pages
     - Detail collector for individual restaurant pages
   - Configures caching (stores data in `cache/scrape/` directory)
   - Sets rate limiting (2-second delay, 1 parallel request)
   - Applies random user agents and referer headers

3. **Target URLs Setup**
   - Defines five main categories to scrape:
     - 3-star Michelin restaurants
     - 2-star Michelin restaurants  
     - 1-star Michelin restaurants
     - Bib Gourmand restaurants
     - Selected restaurants (The Plate)

#### Backfill Command Initialization

1. **Database Connection**
   - Uses the same SQLite database as scrape command
   - Retrieves existing restaurant URLs for backfill processing

2. **Wayback Machine Configuration**
   - Configures Colly for web.archive.org domain
   - Sets up caching in `cache/wayback/` directory
   - Uses CDX API to find historical snapshots
   - Configured for 3 parallel requests with 1-second delay

### Phase 2: Data Extraction Process

#### Step 1: Category Page Scraping (Scrape Command)

The scraper visits each category URL and extracts:

- **Restaurant Cards**: Using XPath selectors defined in `internal/scraper/xpath.go`
- **Restaurant URLs**: Links to individual restaurant detail pages
- **Location Information**: Geographic location and coordinates from data attributes
- **Award Classification**: Determined by the source URL mapping

#### Step 2: Pagination Handling

- **Next Page Detection**: Uses XPath selectors for pagination buttons
- **Automatic Navigation**: Follows pagination links until all pages are processed
- **State Management**: Maintains context (location, award type) across page transitions

#### Step 3: Restaurant Detail Extraction

For each restaurant, the scraper extracts:

1. **Basic Information**
   - Restaurant name
   - Full address
   - Website URL
   - Description

2. **Pricing and Cuisine**
   - Price categories (mapped from CAT_P01-P04 to $-$$$$)
   - Cuisine type
   - Green Star sustainability award detection

3. **Geographic Data**
   - Latitude and longitude from page data attributes
   - Location information for geographic grouping

4. **Contact Information**
   - Phone number extraction and formatting
   - International format conversion using E164 standard

5. **Award Information**
   - Award distinction (3 Stars, 2 Stars, 1 Star, Bib Gourmand, Selected)
   - Published year from JSON-LD structured data
   - Green Star sustainability certification

#### Step 4: Historical Data Backfill (Backfill Command)

The backfill process retrieves historical restaurant data:

1. **Wayback Machine Integration**
   - Uses CDX API to find historical snapshots
   - Retrieves archived versions of restaurant pages
   - Processes multiple years of data for each restaurant

2. **Historical Data Processing**
   - Extracts award information from archived pages
   - Parses different page formats from various years
   - Maintains data integrity with wayback URL references

3. **Smart Data Merging**
   - Prevents backfill data from overwriting fresher scrape data
   - Maintains historical timeline of restaurant awards
   - Handles data conflicts intelligently

## Backfill Functionality - Historical Data Collection

### Overview

The backfill functionality is a unique feature that retrieves historical restaurant data from the Wayback Machine (web.archive.org). This allows the application to build a comprehensive timeline of restaurant awards and changes over the years.

### How Backfill Works

#### Phase 1: Snapshot Discovery
1. **CDX API Integration**: Queries the Wayback Machine's CDX API for available snapshots
2. **Timestamp Collection**: Retrieves all available timestamps for each restaurant URL
3. **Snapshot Filtering**: Filters out malformed or incomplete snapshots
4. **URL Generation**: Creates Wayback Machine URLs for each valid snapshot

#### Phase 2: Historical Data Extraction
1. **Archived Page Retrieval**: Fetches archived versions of restaurant pages
2. **Multi-Format Parsing**: Handles different page layouts from various years
3. **Data Extraction**: Extracts award information from historical pages
4. **Year Detection**: Identifies the publication year of each award

#### Phase 3: Data Integration
1. **Conflict Resolution**: Prevents backfill data from overwriting fresh scrape data
2. **Timeline Building**: Creates historical award timelines for each restaurant
3. **Data Validation**: Ensures historical data meets quality standards
4. **Database Storage**: Stores historical awards with wayback URL references

### Usage Examples

#### Backfill All Restaurants
```bash
go run cmd/mym/mym.go backfill
```

#### Backfill Specific Restaurant
```bash
go run cmd/mym/mym.go backfill https://guide.michelin.com/en/restaurants/restaurant-url
```

#### Backfill with Logging
```bash
go run cmd/mym/mym.go backfill -log debug
```

### Benefits of Historical Data

1. **Award Timeline Tracking**: See when restaurants gained or lost stars
2. **Historical Analysis**: Analyze trends in Michelin awards over time
3. **Data Completeness**: Build comprehensive restaurant profiles
4. **Research Applications**: Support academic and industry research
5. **Verification**: Cross-reference current data with historical records

### Technical Implementation

The backfill system is implemented in `internal/backfill/` with the following components:

- **`backfill.go`**: Main backfill orchestration logic
- **`extract.go`**: Historical data extraction functions
- **`extract_test.go`**: Test coverage for extraction logic

### Phase 3: Data Processing and Parsing

#### Parser Utilities (`internal/parser/parser.go`)

The parser package provides several specialized functions:

1. **`SplitUnpack()`**: Splits strings by separator and returns two components
   - Used for price/cuisine separation
   - Handles missing components gracefully

2. **`TrimWhiteSpaces()`**: Cleans extracted text
   - Removes line breaks and excessive spaces
   - Normalizes whitespace characters

3. **`ParsePublishedYearFromJSONLD()`**: Extracts publication year from JSON-LD
   - Parses structured data from restaurant pages
   - Handles various date formats and layouts
   - Returns year for award timeline tracking

4. **`ParseDistinction()`**: Processes award distinction strings
   - Maps various award formats to standardized values
   - Handles HTML entities and formatting variations
   - Returns consistent award classifications

5. **`ParseGreenStar()`**: Detects sustainability awards
   - Identifies Michelin Green Star designations
   - Boolean flag for sustainability certification

6. **`ParsePhoneNumber()`**: Processes phone numbers
   - International format conversion using E164 standard
   - Handles various international number formats

7. **`MapPrice()`**: Maps price categories
   - Converts CAT_P01-P04 to $-$$$$ format
   - Standardizes price representation

#### Data Model (`internal/models/`)

The application uses two main data models:

##### Restaurant Model (`restaurant.go`)
```go
type Restaurant struct {
    ID                    uint   `gorm:"primaryKey"`
    URL                   string `gorm:"unique;not null;index"`
    Name                  string `gorm:"index:idx_name"`
    Description           string `gorm:"not null"`
    Address               string `gorm:"not null"`
    Location              string `gorm:"not null;index:idx_location"`
    Latitude              string `gorm:"not null"`
    Longitude             string `gorm:"not null"`
    Cuisine               string `gorm:"not null"`
    PhoneNumber           string
    FacilitiesAndServices string
    WebsiteURL            string
    Awards                []RestaurantAward `gorm:"foreignKey:RestaurantID"`
    CreatedAt             time.Time
    UpdatedAt             time.Time
}
```

##### Award Model (`award.go`)
```go
type RestaurantAward struct {
    ID           uint   `gorm:"primaryKey"`
    RestaurantID uint   `gorm:"not null;index"`
    Year         int    `gorm:"not null"`
    Distinction  string `gorm:"not null"`
    Price        string `gorm:"not null"`
    GreenStar    bool
    WaybackURL   string
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

#### Database Storage with GORM

The application uses GORM for database operations:

- **Auto-migration**: Automatically creates and updates database schema
- **Relationships**: Proper foreign key relationships between restaurants and awards
- **Validation**: Built-in validation for required fields and data integrity
- **Upsert Operations**: Smart merging of new and existing data
- **Indexing**: Optimized database performance with strategic indexes

### Phase 4: Output Generation

#### Primary Database Storage

The application stores all data in a SQLite database (`data/michelin.db`) with two main tables:

1. **restaurants**: Core restaurant information
   - Primary key: ID (auto-increment)
   - Unique constraint: URL
   - Indexes: Name, Location for efficient querying

2. **restaurant_awards**: Historical award data
   - Primary key: ID (auto-increment)
   - Foreign key: RestaurantID
   - Unique constraint: (RestaurantID, Year) for award timeline
   - Indexes: Year, Distinction for filtering

#### CSV Export Generation

A CSV file can be generated from the database using the `sqlitetocsv` make target:

```bash
make sqlitetocsv
```

The exported CSV contains the following columns:
- Name, Address, Location
- Price (current year), Cuisine
- Longitude, Latitude
- PhoneNumber, Url, WebsiteUrl
- Award, GreenStar
- FacilitiesAndServices, Description

#### Data Quality Assurance

- **Database Constraints**: Ensures data integrity through foreign keys and validation
- **Phone Number Formatting**: Standardizes international phone numbers to E164 format
- **Error Handling**: Comprehensive logging with structured error reporting
- **Empty Value Handling**: Graceful handling of missing data with appropriate defaults
- **Duplicate Prevention**: URL-based deduplication prevents duplicate restaurants
- **Historical Data Integrity**: Smart merging prevents backfill from overwriting fresh data

## Configuration and Customization

### Application Configuration

#### Target URLs (`internal/models/award.go`)
```go
var DistinctionURL = map[string]string{
    ThreeStars:          "https://guide.michelin.com/en/restaurants/3-stars-michelin",
    TwoStars:            "https://guide.michelin.com/en/restaurants/2-stars-michelin",
    OneStar:             "https://guide.michelin.com/en/restaurants/1-star-michelin",
    BibGourmand:         "https://guide.michelin.com/en/restaurants/bib-gourmand",
    SelectedRestaurants: "https://guide.michelin.com/en/restaurants/the-plate-michelin",
}
```

#### Scraping Parameters

##### Scrape Command Configuration
- **Rate Limiting**: 2-second delay between requests
- **Parallelism**: 1 concurrent request (conservative approach)
- **Caching**: Enabled by default in `cache/scrape/` directory
- **Database**: SQLite storage at `data/michelin.db`
- **Max URLs**: 30,000 URLs per session
- **Max Retries**: 3 attempts per failed request

##### Backfill Command Configuration
- **Rate Limiting**: 1-second delay between requests
- **Parallelism**: 3 concurrent requests maximum
- **Caching**: Enabled by default in `cache/wayback/` directory
- **Target Domain**: `web.archive.org`
- **Max URLs**: 300,000 URLs per session (includes historical snapshots)

#### XPath Selectors (`internal/scraper/xpath.go`)

The application uses XPath selectors for precise element targeting:

- **Restaurant Cards**: Defined in xpath.go for listing pages
- **Restaurant Names**: Selectors for restaurant detail extraction
- **Addresses**: Location and address information selectors
- **Price/Cuisine**: Award and pricing information selectors
- **Coordinates**: Geographic data extraction selectors
- **Phone Numbers**: Contact information selectors
- **Website URLs**: External link selectors
- **Award Information**: JSON-LD and award distinction selectors

## Performance and Optimization

### Caching Strategy

- **Scrape Cache**: Stores HTTP responses in `cache/scrape/` directory
- **Backfill Cache**: Stores Wayback Machine responses in `cache/wayback/` directory
- **Cache Size**: Varies by usage, typically several GB for full dataset
- **Development Efficiency**: Subsequent runs complete much faster with cache
- **Cache Management**: Delete respective cache directories to clear

### Database Optimization

- **SQLite Configuration**: Uses WAL mode for better concurrency
- **PRAGMA Settings**: Optimized for performance with memory-based temp storage
- **Connection Pooling**: Efficient database connection management
- **Strategic Indexing**: Indexes on frequently queried columns (URL, Location, Name)
- **Upsert Operations**: Efficient data merging with ON CONFLICT clauses

### Rate Limiting

- **Respectful Scraping**: 2-second delay for scraping, 1-second for backfill
- **Random Delays**: Additional randomization to avoid detection
- **Concurrent Limits**: 1 parallel request for scraping, 3 for backfill
- **Domain Restrictions**: Limited to `guide.michelin.com` and `web.archive.org`
- **Request Retry**: 3 attempts per failed request with backoff

### Error Handling

- **Graceful Degradation**: Continues processing despite individual failures
- **Structured Logging**: Comprehensive error and progress logging with logrus
- **Data Validation**: Database constraints and model validation
- **Resource Management**: Proper cleanup of database connections and HTTP clients
- **Transaction Safety**: Database operations wrapped in transactions where appropriate

## Testing and Quality Assurance

### Unit Testing

The application includes comprehensive unit tests for:

1. **Parser Functions** (`internal/parser/parser_test.go`):
   - String splitting and unpacking
   - JSON-LD parsing for publication years
   - Award distinction parsing
   - Phone number formatting
   - Price category mapping

2. **Scraper Functions** (`internal/scraper/scraper_test.go`):
   - XPath selector validation
   - Data extraction logic
   - Error handling scenarios

3. **Extraction Functions** (`internal/backfill/extract_test.go`):
   - Historical data extraction
   - Wayback Machine response parsing
   - Data format handling

### Test Coverage

Run tests with:
```bash
make test  # go test ./... -v -count=1
```

### Linting

Code quality is maintained with:
```bash
make lint  # golangci-lint run -v
```

### Development Testing

For development and testing purposes:

#### Limited Restaurant Extraction
You can extract a limited number of random restaurants for testing without creating the full database:

```bash
# Extract only 10 random restaurants for testing
go run cmd/mym/mym.go scrape -limit 10

# Extract 25 restaurants with debug logging
go run cmd/mym/mym.go scrape -limit 25 -log debug

# Extract 5 restaurants for quick testing
go run cmd/mym/mym.go scrape -limit 5
```

The limit feature:
- **Randomization**: Uses probabilistic sampling to get a diverse mix of restaurants across different categories
- **Progress Tracking**: Shows progress towards the limit with detailed logging
- **Database Storage**: Creates the same database structure as full scraping
- **Fast Testing**: Completes in minutes instead of hours

#### Database Inspection
```bash
# View database with datasette
make datasette

# Convert database to CSV for inspection
make sqlitetocsv
```

#### Partial Scraping
You can also test with specific restaurant URLs using the backfill command:

```bash
# Test backfill with specific restaurant URL
go run cmd/mym/mym.go backfill https://guide.michelin.com/en/restaurants/restaurant-url
```

#### Test Data Management
- Test data is stored in `internal/scraper/testdata/`
- Includes sample HTML files for testing extraction logic
- Capture scripts for updating test data when needed

## Adaptation Guide

### For Different Websites

To adapt this scraper for other restaurant or business listing websites:

1. **Update Target URLs**: Modify the `DistinctionURL` map in `internal/models/award.go`
2. **Adjust XPath Selectors**: Update selectors in `internal/scraper/xpath.go`
3. **Modify Data Structure**: Update the `Restaurant` and `RestaurantAward` models
4. **Customize Parsing**: Modify parser functions in `internal/parser/parser.go`
5. **Update Award Logic**: Adjust award classification logic for different systems
6. **Database Schema**: Update GORM models and migrations for new fields

### For Different Data Sources

1. **Change Output Format**: Add JSON/XML export alongside CSV functionality
2. **Add Data Fields**: Extend the models with additional fields and database columns
3. **Implement Different Protocols**: Add support for APIs, different authentication
4. **Database Options**: Replace SQLite with PostgreSQL/MySQL by updating storage layer
5. **External APIs**: Add integration with external services for data enrichment

### For Different Scraping Patterns

1. **Single-Page Applications**: Add JavaScript rendering support via chromedp
2. **API-Based Sites**: Implement JSON API client instead of HTML scraping
3. **Multi-Language Support**: Add language-specific URL and selector handling
4. **Dynamic Content**: Implement waiting strategies for AJAX-loaded content
5. **Authentication**: Add login/session management for protected content

## Best Practices and Considerations

### Ethical Scraping

- **Respect robots.txt**: Check and comply with site policies
- **Rate Limiting**: Implement reasonable delays between requests
- **User Agent**: Use descriptive, contact-able user agent strings
- **Caching**: Avoid unnecessary repeated requests

### Legal Considerations

- **Terms of Service**: Review and comply with website terms
- **Data Usage**: Understand restrictions on data usage and redistribution
- **Attribution**: Provide proper attribution when required
- **Privacy**: Handle personal data (phone numbers, addresses) appropriately

### Maintenance

- **Selector Updates**: Regularly check and update XPath selectors
- **Error Monitoring**: Monitor logs for parsing errors and failures
- **Data Validation**: Regularly validate output data quality
- **Performance Monitoring**: Track scraping performance and adjust parameters

## Troubleshooting

### Common Issues

1. **Empty Database**: Check XPath selectors for website changes
2. **Missing Data**: Verify website structure hasn't changed
3. **Rate Limiting**: Adjust delays if receiving 429 errors
4. **Cache Issues**: Clear cache directories if seeing stale data
5. **Database Errors**: Check SQLite file permissions and disk space
6. **Backfill Failures**: Verify Wayback Machine availability and URL format

### Debug Tips

1. **Enable Verbose Logging**: Use `-log debug` flag for detailed output
2. **Test Individual Components**: Use unit tests to isolate issues
3. **Inspect Network Traffic**: Use browser dev tools to understand site behavior
4. **Validate XPath**: Use browser console to test XPath expressions
5. **Check Dependencies**: Ensure all Go modules are up to date
6. **Database Inspection**: Use `make datasette` to inspect database contents
7. **Cache Management**: Clear specific cache directories for troubleshooting

### Database Issues

1. **Database Locked**: Ensure no other processes are accessing the database
2. **Disk Space**: Check available disk space for database and cache
3. **Permissions**: Verify write permissions for data directory
4. **Corruption**: Use SQLite CLI to check database integrity

### Backfill Issues

1. **Wayback Machine Unavailable**: Check web.archive.org accessibility
2. **Rate Limiting**: Reduce concurrent requests in backfill configuration
3. **Snapshot Parsing**: Historical pages may have different formats
4. **Data Conflicts**: Review conflict resolution logic for data integrity

## Dependencies and Setup

### Required Dependencies

```go
github.com/gocolly/colly/v2 v2.2.0
github.com/nyaruka/phonenumbers v1.6.3
github.com/sirupsen/logrus v1.9.3
github.com/stretchr/testify v1.10.0
gorm.io/driver/sqlite v1.6.0
gorm.io/gorm v1.30.0
```

### Setup Instructions

1. **Install Go**: Ensure Go 1.24+ is installed
2. **Clone Repository**: `git clone <repository-url>`
3. **Install Dependencies**: `go mod download`
4. **Run Scraper**: `go run cmd/mym/mym.go scrape`
5. **Run Backfill**: `go run cmd/mym/mym.go backfill`
6. **Build Binary**: `make build`

### Available Make Commands

```bash
make help          # Display available commands
make test          # Run all tests
make lint          # Run linting
make build         # Build binary
make scrape        # Run scraping command
make test-scrape   # Scrape 10 random restaurants for testing
make datasette     # View database with datasette
make sqlitetocsv   # Convert database to CSV
make docker-build  # Build Docker image
make docker-run    # Run in Docker
```

### Environment Requirements

- **Go 1.24+**: Required for latest language features
- **Internet Connection**: Required for web scraping and Wayback Machine
- **Disk Space**: Several GB for cache storage and database
- **Memory**: Moderate requirements, optimized for single-threaded scraping
- **Optional**: SQLite3 CLI for database inspection
- **Optional**: Datasette for database visualization

This documentation provides a comprehensive understanding of the Michelin My Maps scraper architecture and implementation details for version 3.2.1. The application has evolved significantly from earlier versions, now featuring:

- **Robust Database Storage**: SQLite database with GORM ORM for reliable data persistence
- **Historical Data Collection**: Unique backfill functionality using Wayback Machine
- **Structured CLI Interface**: Subcommands for different operational modes
- **Comprehensive Testing**: Unit tests and development tools for quality assurance
- **Production Ready**: Docker support, proper error handling, and performance optimization

Use this guide to understand the codebase, contribute to the project, or adapt it for your specific requirements. The modular architecture makes it easy to extend functionality or adapt for different data sources. 