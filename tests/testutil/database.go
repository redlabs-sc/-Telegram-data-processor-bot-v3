package testutil

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"

	_ "github.com/lib/pq"
)

// TestDBConfig holds test database configuration
type TestDBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

// DefaultTestDBConfig returns default test database config
func DefaultTestDBConfig() *TestDBConfig {
	return &TestDBConfig{
		Host:     "localhost",
		Port:     5432,
		User:     "bot_user",
		Password: "change_me_in_production",
		DBName:   "telegram_bot_test",
	}
}

// SetupTestDB creates a test database connection and runs migrations
func SetupTestDB(t *testing.T, cfg *TestDBConfig) *sql.DB {
	if cfg == nil {
		cfg = DefaultTestDBConfig()
	}

	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}

	if err := db.Ping(); err != nil {
		t.Fatalf("Failed to ping test database: %v", err)
	}

	// Run migrations
	if err := runMigrations(db); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	return db
}

// CleanupTestDB cleans up test database
func CleanupTestDB(t *testing.T, db *sql.DB) {
	// Truncate all tables
	tables := []string{
		"download_queue",
		"batch_processing",
		"batch_files",
		"metrics",
	}

	for _, table := range tables {
		_, err := db.Exec(fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
		if err != nil {
			t.Logf("Warning: Failed to truncate table %s: %v", table, err)
		}
	}

	db.Close()
}

// runMigrations runs database migrations from migration files
func runMigrations(db *sql.DB) error {
	migrationDir := "../../database/migrations"

	files, err := filepath.Glob(filepath.Join(migrationDir, "*.sql"))
	if err != nil {
		return fmt.Errorf("failed to list migration files: %w", err)
	}

	for _, file := range files {
		content, err := ioutil.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file, err)
		}

		if _, err := db.Exec(string(content)); err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", file, err)
		}
	}

	return nil
}

// InsertTestFile inserts a test file into download_queue
func InsertTestFile(t *testing.T, db *sql.DB, fileID, filename, fileType string, status string) int64 {
	var taskID int64
	err := db.QueryRow(`
		INSERT INTO download_queue (file_id, user_id, filename, file_type, file_size, status)
		VALUES ($1, 1, $2, $3, 1024, $4)
		RETURNING task_id
	`, fileID, filename, fileType, status).Scan(&taskID)

	if err != nil {
		t.Fatalf("Failed to insert test file: %v", err)
	}

	return taskID
}

// InsertTestBatch inserts a test batch into batch_processing
func InsertTestBatch(t *testing.T, db *sql.DB, batchID string, fileCount int, status string) {
	_, err := db.Exec(`
		INSERT INTO batch_processing (batch_id, file_count, status, created_at)
		VALUES ($1, $2, $3, NOW())
	`, batchID, fileCount, status)

	if err != nil {
		t.Fatalf("Failed to insert test batch: %v", err)
	}
}

// CountRows counts rows in a table matching a condition
func CountRows(t *testing.T, db *sql.DB, table, condition string) int {
	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", table, condition)
	err := db.QueryRow(query).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count rows in %s: %v", table, err)
	}
	return count
}
