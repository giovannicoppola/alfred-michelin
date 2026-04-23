#!/bin/bash

# Check if required arguments are provided
if [ $# -lt 2 ]; then
    echo "Error: app name and output directory are required"
    echo "Usage: $0 <app_name> <output_directory> [<db_source_path>]"
    echo "  If <db_source_path> is provided, it is zipped into"
    echo "  <output_directory>/michelin.db.zip for end-user distribution."
    exit 1
fi

APP_NAME="$1"
OUTPUT_DIR="$2"
DB_SOURCE_PATH="${3:-}"

# Validate app name (basic check for valid characters)
if ! [[ "$APP_NAME" =~ ^[a-zA-Z0-9_-]+$ ]]; then
    echo "Error: Invalid app name: $APP_NAME"
    echo "The name should contain only letters, numbers, underscores, and hyphens"
    exit 1
fi

# Create output directory if it doesn't exist
mkdir -p "$OUTPUT_DIR"

# Clean up any existing binaries
rm -f "${OUTPUT_DIR}/${APP_NAME}" "${OUTPUT_DIR}/${APP_NAME}_x86_64" "${OUTPUT_DIR}/${APP_NAME}_arm64"

echo "Compiling for x86_64..."
CGO_ENABLED=1 GOARCH=amd64 GOOS=darwin go build -o "${OUTPUT_DIR}/${APP_NAME}_x86_64" . || {
    echo "Error: Failed to compile for x86_64"
    exit 1
}

echo "Compiling for arm64..."
CGO_ENABLED=1 GOARCH=arm64 GOOS=darwin go build -o "${OUTPUT_DIR}/${APP_NAME}_arm64" . || {
    echo "Error: Failed to compile for arm64"
    exit 1
}

echo "Creating universal binary..."
lipo -create -output "${OUTPUT_DIR}/${APP_NAME}" "${OUTPUT_DIR}/${APP_NAME}_x86_64" "${OUTPUT_DIR}/${APP_NAME}_arm64" || {
    echo "Error: Failed to create universal binary"
    exit 1
}

# Clean up intermediate files
rm -f "${OUTPUT_DIR}/${APP_NAME}_x86_64" "${OUTPUT_DIR}/${APP_NAME}_arm64"

echo "Done! Universal binary created: ${OUTPUT_DIR}/${APP_NAME}"
lipo -info "${OUTPUT_DIR}/${APP_NAME}"

# Optionally zip the scraped database for distribution.
if [ -n "$DB_SOURCE_PATH" ]; then
    if [ ! -f "$DB_SOURCE_PATH" ]; then
        echo "Error: db source not found: $DB_SOURCE_PATH"
        exit 1
    fi

    # Resolve to absolute paths before changing directories — the zip step
    # cd's into either the db's parent or a staging dir, so any relative
    # OUTPUT_DIR or DB_SOURCE_PATH would break once the cwd changes.
    ABS_OUTPUT_DIR=$(cd "$OUTPUT_DIR" && pwd)
    ABS_DB_SOURCE=$(cd "$(dirname "$DB_SOURCE_PATH")" && pwd)/$(basename "$DB_SOURCE_PATH")
    ZIP_PATH="${ABS_OUTPUT_DIR}/michelin.db.zip"
    rm -f "$ZIP_PATH"

    DB_FILE=$(basename "$ABS_DB_SOURCE")

    if [ "$DB_FILE" != "michelin.db" ]; then
        # initializeDatabase in pkg/main.go looks for "michelin.db" inside the
        # archive; stage a copy under that name so a scraper that writes a
        # differently-named file still produces a correct archive.
        STAGE_DIR=$(mktemp -d -t michelin_db_stage)
        trap 'rm -rf "$STAGE_DIR"' EXIT
        cp "$ABS_DB_SOURCE" "$STAGE_DIR/michelin.db"
        (cd "$STAGE_DIR" && zip -q "$ZIP_PATH" "michelin.db") || {
            echo "Error: failed to zip database"
            exit 1
        }
    else
        (cd "$(dirname "$ABS_DB_SOURCE")" && zip -q "$ZIP_PATH" "$DB_FILE") || {
            echo "Error: failed to zip database"
            exit 1
        }
    fi

    ZIP_SIZE=$(du -h "$ZIP_PATH" | cut -f1)
    echo "Database zipped: ${ZIP_PATH} (${ZIP_SIZE})"
fi
