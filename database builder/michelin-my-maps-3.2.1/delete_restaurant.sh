#!/bin/bash

if [ $# -eq 0 ]; then
    echo "Usage: $0 <restaurant_id>"
    exit 1
fi

RESTAURANT_ID=$1
DB_PATH="/Users/giovanni/Library/Application Support/Alfred/Workflow Data/com.giovanni.alfred-michelin/michelin.db"

echo "Deleting restaurant with ID: $RESTAURANT_ID"
echo "Are you sure? (y/N)"
read -r response

if [[ "$response" =~ ^[Yy]$ ]]; then
    sqlite3 "$DB_PATH" << EOF
DELETE FROM user_visits WHERE restaurant_id = $RESTAURANT_ID;
DELETE FROM user_favorites WHERE restaurant_id = $RESTAURANT_ID;
DELETE FROM restaurant_awards WHERE restaurant_id = $RESTAURANT_ID;
DELETE FROM restaurants WHERE id = $RESTAURANT_ID;
EOF
    echo "Restaurant deleted successfully!"
else
    echo "Operation cancelled."
fi
