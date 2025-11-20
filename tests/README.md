# Testing Guide

This directory contains tests for the Telegram Data Processor Bot (Corrected Architecture).

## Test Structure

```
tests/
├── integration/       # Integration tests for end-to-end pipeline
├── testutil/         # Test utilities and helpers
└── README.md         # This file
```

## Running Tests

### Unit Tests

Run unit tests for individual packages:

```bash
# Test config package
go test ./config -v

# Test all packages
go test ./... -v

# Run with coverage
go test ./... -cover
```

### Integration Tests

Integration tests require a test database:

```bash
# Skip integration tests (for quick unit testing)
go test ./... -short

# Run integration tests
go test ./tests/integration -v

# Run specific integration test
go test ./tests/integration -run TestBatchPipelineBasic -v
```

### Load Tests

Load tests verify performance under high load:

```bash
# Run load test with 100 files
go test ./tests/load -run TestLoad100Files -v -timeout 3h

# Run with custom parameters
LOAD_TEST_FILES=1000 go test ./tests/load -v -timeout 5h
```

## Test Database Setup

Integration tests require a PostgreSQL test database:

```bash
# Create test database
createdb telegram_bot_test

# Set database credentials (if different from defaults)
export TEST_DB_HOST=localhost
export TEST_DB_PORT=5432
export TEST_DB_USER=bot_user
export TEST_DB_PASSWORD=change_me_in_production
export TEST_DB_NAME=telegram_bot_test
```

## Test Coverage

Generate test coverage report:

```bash
# Generate coverage profile
go test ./... -coverprofile=coverage.out

# View coverage in browser
go tool cover -html=coverage.out

# View coverage summary
go tool cover -func=coverage.out
```

## Writing Tests

### Unit Tests

Unit tests should be placed in `*_test.go` files next to the code they test:

```go
package mypackage

import "testing"

func TestMyFunction(t *testing.T) {
    result := MyFunction()
    if result != expected {
        t.Errorf("got %v, want %v", result, expected)
    }
}
```

### Integration Tests

Integration tests go in `tests/integration/`:

```go
package integration

import (
    "testing"
    "github.com/redlabs-sc/telegram-data-processor-bot-v3/tests/testutil"
)

func TestFeature(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    db := testutil.SetupTestDB(t, nil)
    defer testutil.CleanupTestDB(t, db)

    // Test implementation
}
```

## Test Utilities

The `testutil` package provides helpers for testing:

- `SetupTestDB()` - Creates test database connection
- `CleanupTestDB()` - Cleans up test database
- `InsertTestFile()` - Inserts test file into download_queue
- `InsertTestBatch()` - Inserts test batch into batch_processing
- `CountRows()` - Counts rows matching condition

## Continuous Integration

Tests run automatically on every commit via CI/CD:

```yaml
# .github/workflows/test.yml
- name: Run tests
  run: go test ./... -v

- name: Run integration tests
  run: go test ./tests/integration -v
```

## Performance Benchmarks

Benchmark critical code paths:

```bash
# Run benchmarks
go test ./... -bench=. -benchmem

# Run specific benchmark
go test ./internal/workers -bench=BenchmarkExtractWorker -benchmem
```

## Test Targets

From implementation plan Phase 6:

- ✅ Unit tests for config package
- ⚠️ Unit tests for batch coordinator (placeholder)
- ⚠️ Integration tests for pipeline (placeholder)
- ❌ Load testing (100+ files)
- ❌ Performance profiling
- ❌ Resource usage verification

## Architecture-Specific Tests

### Mutex Constraint Tests

Verify that extract and convert stages respect mutex constraints:

```bash
go test ./tests/integration -run TestMutexConstraints -v
```

Expected behavior:
- Never more than 1 batch in EXTRACTING status
- Never more than 1 batch in CONVERTING status
- Up to 5 batches in STORING status (batch isolation)

### Batch Isolation Tests

Verify that concurrent store workers don't conflict:

```bash
go test ./tests/integration -run TestConcurrentStoreWorkers -v
```

Expected behavior:
- 5 store workers process different batches simultaneously
- No file conflicts (each batch has isolated directory)
- Database UNIQUE constraints handle duplicate inserts

## Troubleshooting

### Test Database Connection Fails

```bash
# Verify PostgreSQL is running
pg_isready

# Check database exists
psql -l | grep telegram_bot_test

# Create database if missing
createdb telegram_bot_test
```

### Integration Tests Timeout

```bash
# Increase timeout
go test ./tests/integration -v -timeout 10m
```

### Tests Fail with "no such table"

```bash
# Migrations may not have run
# Check testutil/database.go runMigrations() function
```

## Test Data

Test fixtures are created programmatically using `testutil` helpers.
No static test data files are required.

## Notes

- Integration tests marked with `testing.Short()` skip
- Load tests require significant time (2-3 hours for 100 files)
- Performance tests should run on dedicated hardware
- Mutex violation tests are critical for architecture correctness
