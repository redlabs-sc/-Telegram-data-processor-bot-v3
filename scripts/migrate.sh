#!/bin/bash
set -e

# Database Migration Script
# Runs all SQL migrations in order

# Check if .env exists
if [ ! -f .env ]; then
    echo "❌ Error: .env file not found"
    echo "Please create .env file with database configuration"
    exit 1
fi

# Load environment variables
export $(cat .env | grep -v '^#' | xargs)

# Verify required environment variables
required_vars=("DB_HOST" "DB_PORT" "DB_NAME" "DB_USER" "DB_PASSWORD")
for var in "${required_vars[@]}"; do
    if [ -z "${!var}" ]; then
        echo "❌ Error: $var is not set in .env"
        exit 1
    fi
done

echo "🔄 Running database migrations..."
echo "Database: $DB_NAME on $DB_HOST:$DB_PORT"
echo ""

# Run migrations in order
migration_count=0
for migration in database/migrations/*.sql; do
    if [ -f "$migration" ]; then
        echo "▶ Applying: $(basename $migration)"
        PGPASSWORD=$DB_PASSWORD psql -U $DB_USER -h $DB_HOST -p $DB_PORT -d $DB_NAME -f $migration
        ((migration_count++))
        echo "✅ $(basename $migration) completed"
        echo ""
    fi
done

echo "✅ All $migration_count migrations completed successfully!"
echo ""
echo "Verifying tables..."
PGPASSWORD=$DB_PASSWORD psql -U $DB_USER -h $DB_HOST -p $DB_PORT -d $DB_NAME -c "\dt"
