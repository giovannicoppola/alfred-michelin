#!/bin/bash

# Script to build and package the Alfred Michelin workflow
set -e

# Navigate to the project root
cd "$(dirname "$0")/.."
PROJECT_ROOT=$(pwd)

echo "Building Go application..."
cd src
go build -o ../workflow/michelin

echo "Copying CSV file to the workflow directory..."
cp "$PROJECT_ROOT/michelin_my_maps.csv" "$PROJECT_ROOT/workflow/"

# Get the version from the workflow info.plist file
VERSION=$(grep -A 1 "<key>version</key>" "$PROJECT_ROOT/workflow/info.plist" | grep "<string>" | sed 's/<string>\(.*\)<\/string>/\1/' | tr -d '[:space:]')

echo "Creating workflow package: v$VERSION..."
cd "$PROJECT_ROOT"

# Create the build directory if it doesn't exist
mkdir -p build

# Create the workflow package
cd workflow
zip -r "../build/alfred-michelin-v$VERSION.alfredworkflow" . -x "*.DS_Store"

echo "Package created: build/alfred-michelin-v$VERSION.alfredworkflow" 