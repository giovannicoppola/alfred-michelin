# Alfred Michelin Restaurant Guide

An Alfred workflow to search, favorite, and track your visits to Michelin guide restaurants around the world.

## Features

- Search through 17,000+ Michelin guide restaurants by name, location, or distinction (stars)
- View detailed restaurant information including address, price range, cuisine type, and more
- Save restaurants to your favorites list for quick access
- Track restaurants you've visited with date and personal notes
- Open restaurant websites directly from Alfred
- View restaurant locations on maps

## Commands

- `mr [query]` - Search for Michelin restaurants by name, location, or distinction
- `mf` - View your favorite restaurants
- `mv` - View your visited restaurants
- `mrd [id]` - View detailed information for a specific restaurant

## Installation

1. Download the latest release from the [releases page](https://github.com/giovanni/alfred-michelin/releases)
2. Double-click the `.alfredworkflow` file to install it in Alfred
3. The workflow will automatically set up the database on first use

## Data Source

This workflow uses data from the Michelin Guide, contained in the `michelin_my_maps.csv` file. The data includes:

- Restaurant name
- Address and location
- Price range ($ to $$$$)
- Cuisine type
- Michelin distinctions (stars)
- Links to restaurant websites and Michelin guide pages

## Requirements

- macOS
- [Alfred Powerpack](https://www.alfredapp.com/powerpack/)

## Development

If you want to modify or extend this workflow:

1. Clone this repository
2. Make your changes to the Go code in the `/src` directory
3. Build the application with: `cd src && go build -o ../workflow/michelin`
4. Test the workflow in Alfred

## License

MIT

## Credits

Created by Giovanni

# Usage
# Roadmap


# Acknowledgments
 
