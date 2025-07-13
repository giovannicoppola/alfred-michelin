# Image Scraping Functionality

The mym package now includes image scraping functionality that can extract restaurant images from Michelin Guide pages and save the image URLs to the database.

## Features

- **Single URL scraping**: Scrape image for one restaurant
- **Batch processing**: Scrape images for multiple restaurants from a file
- **Database integration**: Automatically saves image URLs to the restaurant records
- **Respectful scraping**: Includes delays between requests to avoid overwhelming the server
- **Error handling**: Continues processing even if some restaurants fail

## Usage

### Scrape image for a single restaurant

```bash
./mym images -url "https://guide.michelin.com/en/california/us-los-angeles/restaurant/osteria-mozza"
```

### Scrape images for multiple restaurants from a file

Create a text file with restaurant URLs (one per line):

```txt
https://guide.michelin.com/en/california/us-los-angeles/restaurant/osteria-mozza
https://guide.michelin.com/en/california/us-los-angeles/restaurant/providence
https://guide.michelin.com/en/california/us-los-angeles/restaurant/maude
```

Then run:

```bash
./mym images -file restaurants.txt
```

### Set log level for debugging

```bash
./mym images -file restaurants.txt -log debug
```

## Database Schema

The `Restaurant` model now includes an `ImageURL` field that stores the URL to the restaurant's main image:

```go
type Restaurant struct {
    // ... existing fields ...
    ImageURL string // URL to the restaurant's main image
    // ... existing fields ...
}
```

## How it works

1. **Image Detection**: The scraper looks for images in multiple locations on the Michelin page:
   - Meta tags with `og:image` property
   - Data attributes with `data-ci-bg-url`
   - Standard `img` tags

2. **URL Filtering**: Only images from the Michelin CDN (`axwwgrkdco.cloudimg.io`) are considered

3. **Database Update**: The image URL is saved to the restaurant record in the SQLite database

4. **Error Handling**: If a restaurant is not found in the database or image scraping fails, the error is logged but processing continues

## Performance Considerations

- **18,000 restaurants**: With ~18,000 restaurants, downloading all images would require significant storage space
- **On-demand loading**: The current approach saves only the image URLs, allowing images to be loaded on-demand
- **Caching strategy**: You could implement a caching layer that downloads and stores images locally when first requested

## Future Enhancements

- **Image download**: Add option to download images to local storage
- **Image validation**: Verify that image URLs are still valid
- **Batch size limits**: Add configurable batch processing limits
- **Resume functionality**: Ability to resume interrupted scraping sessions 