package batch

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/redlabs-sc/telegram-data-processor-bot-v3/config"
	"go.uber.org/zap"
)

type Coordinator struct {
	cfg    *config.Config
	db     *sql.DB
	logger *zap.Logger
}

func NewCoordinator(cfg *config.Config, db *sql.DB, logger *zap.Logger) *Coordinator {
	return &Coordinator{
		cfg:    cfg,
		db:     db,
		logger: logger,
	}
}

func (bc *Coordinator) Start(ctx context.Context) {
	bc.logger.Info("Batch coordinator started",
		zap.Int("batch_size", bc.cfg.BatchSize),
		zap.Int("batch_timeout_sec", bc.cfg.BatchTimeoutSec))

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			bc.logger.Info("Batch coordinator stopping")
			return
		case <-ticker.C:
			bc.tryCreateBatch(ctx)
		}
	}
}

func (bc *Coordinator) tryCreateBatch(ctx context.Context) {
	// Note: We create batches as long as there are files ready.
	// Stage-specific workers (extract, convert, store) will enforce
	// their own constraints via mutexes and worker counts.
	// This allows batches to queue up for processing.

	// Count queued batches (waiting for extract stage)
	var queuedBatches int
	err := bc.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM batch_processing
		WHERE status = 'QUEUED_EXTRACT'
	`).Scan(&queuedBatches)

	if err != nil {
		bc.logger.Error("Error counting queued batches", zap.Error(err))
		return
	}

	// Limit queue depth to prevent overwhelming the system
	if queuedBatches >= 20 {
		bc.logger.Debug("Too many queued batches", zap.Int("queued", queuedBatches))
		return
	}

	// Get downloaded files waiting for batch
	rows, err := bc.db.QueryContext(ctx, `
		SELECT task_id, filename, file_type, file_size, created_at
		FROM download_queue
		WHERE status = 'DOWNLOADED' AND batch_id IS NULL
		ORDER BY created_at ASC
		LIMIT $1
	`, bc.cfg.BatchSize)

	if err != nil {
		bc.logger.Error("Error querying downloaded files", zap.Error(err))
		return
	}
	defer rows.Close()

	type fileInfo struct {
		TaskID    int64
		Filename  string
		FileType  string
		FileSize  int64
		CreatedAt time.Time
	}

	var files []fileInfo
	var oldestFileTime time.Time

	for rows.Next() {
		var f fileInfo
		if err := rows.Scan(&f.TaskID, &f.Filename, &f.FileType, &f.FileSize, &f.CreatedAt); err != nil {
			bc.logger.Error("Error scanning row", zap.Error(err))
			continue
		}
		files = append(files, f)

		// Track oldest file time for timeout logic
		if oldestFileTime.IsZero() || f.CreatedAt.Before(oldestFileTime) {
			oldestFileTime = f.CreatedAt
		}
	}

	fileCount := len(files)

	// Create batch if:
	// 1. We have enough files (BATCH_SIZE), OR
	// 2. We have some files and oldest file is waiting > BATCH_TIMEOUT_SEC
	batchTimeout := time.Duration(bc.cfg.BatchTimeoutSec) * time.Second
	shouldCreate := fileCount >= bc.cfg.BatchSize ||
		(fileCount > 0 && time.Since(oldestFileTime) > batchTimeout)

	if !shouldCreate {
		if fileCount > 0 {
			bc.logger.Debug("Not enough files for batch",
				zap.Int("file_count", fileCount),
				zap.Duration("oldest_wait", time.Since(oldestFileTime)))
		}
		return
	}

	// Create batch
	batchID := bc.generateBatchID()
	if err := bc.createBatch(ctx, batchID, files); err != nil {
		bc.logger.Error("Error creating batch", zap.Error(err), zap.String("batch_id", batchID))
		return
	}

	bc.logger.Info("Batch created",
		zap.String("batch_id", batchID),
		zap.Int("file_count", fileCount),
		zap.String("status", "QUEUED_EXTRACT"))
}

func (bc *Coordinator) createBatch(ctx context.Context, batchID string, files []fileInfo) error {
	// Start transaction
	tx, err := bc.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Count archive vs txt files
	archiveCount := 0
	txtCount := 0
	for _, f := range files {
		if f.FileType == "TXT" {
			txtCount++
		} else {
			archiveCount++
		}
	}

	// Create batch record with QUEUED_EXTRACT status (corrected architecture)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO batch_processing (batch_id, file_count, archive_count, txt_count, status)
		VALUES ($1, $2, $3, $4, 'QUEUED_EXTRACT')
	`, batchID, len(files), archiveCount, txtCount)

	if err != nil {
		return fmt.Errorf("insert batch record: %w", err)
	}

	// Update download_queue with batch_id
	for _, f := range files {
		_, err := tx.ExecContext(ctx, `
			UPDATE download_queue
			SET batch_id = $2
			WHERE task_id = $1
		`, f.TaskID, batchID)

		if err != nil {
			return fmt.Errorf("update download_queue: %w", err)
		}

		// Insert into batch_files
		_, err = tx.ExecContext(ctx, `
			INSERT INTO batch_files (batch_id, task_id, file_type, processing_status)
			VALUES ($1, $2, $3, 'PENDING')
		`, batchID, f.TaskID, f.FileType)

		if err != nil {
			return fmt.Errorf("insert batch_files: %w", err)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return err
	}

	// Create batch directory structure
	if err := bc.createBatchDirectories(batchID); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}

	// Move files to batch directories
	for _, f := range files {
		sourcePath := filepath.Join("downloads", fmt.Sprintf("%d_%s", f.TaskID, f.Filename))

		var destPath string
		if f.FileType == "TXT" {
			// TXT files go directly to pass directory (already text)
			destPath = filepath.Join("batches", batchID, "app", "extraction", "files", "pass", f.Filename)
		} else {
			// Archives go to all directory for extraction
			destPath = filepath.Join("batches", batchID, "downloads", f.Filename)
		}

		if err := os.Rename(sourcePath, destPath); err != nil {
			bc.logger.Error("Error moving file",
				zap.Error(err),
				zap.String("source", sourcePath),
				zap.String("dest", destPath))
			continue
		}

		bc.logger.Debug("File moved to batch",
			zap.Int64("task_id", f.TaskID),
			zap.String("filename", f.Filename),
			zap.String("dest", destPath))
	}

	return nil
}

func (bc *Coordinator) createBatchDirectories(batchID string) error {
	batchRoot := filepath.Join("batches", batchID)

	// Create batch directory structure matching extract.go expectations
	dirs := []string{
		filepath.Join(batchRoot, "downloads"),                           // Input: archive files
		filepath.Join(batchRoot, "app", "extraction", "files", "pass"),  // Output: extracted text files
		filepath.Join(batchRoot, "app", "extraction", "files", "nopass"), // Failed extractions
		filepath.Join(batchRoot, "app", "extraction", "files", "error"), // Errors
		filepath.Join(batchRoot, "logs"), // Batch-specific logs
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Copy pass.txt to batch directory (if exists)
	passFile := filepath.Join("app", "extraction", "pass.txt")
	batchPassFile := filepath.Join(batchRoot, "app", "extraction", "pass.txt")

	if _, err := os.Stat(passFile); err == nil {
		// Read source
		data, err := os.ReadFile(passFile)
		if err != nil {
			bc.logger.Warn("Error reading pass.txt", zap.Error(err))
		} else {
			// Write to batch
			if err := os.WriteFile(batchPassFile, data, 0644); err != nil {
				bc.logger.Warn("Error copying pass.txt to batch", zap.Error(err))
			}
		}
	}

	bc.logger.Debug("Batch directories created", zap.String("batch_id", batchID))
	return nil
}

func (bc *Coordinator) generateBatchID() string {
	// Generate unique batch ID based on timestamp and counter
	timestamp := time.Now().Format("20060102_150405")

	// Query max batch number for today
	var maxNum int
	bc.db.QueryRow(`
		SELECT COALESCE(MAX(CAST(SUBSTRING(batch_id FROM LENGTH(batch_id)-2) AS INTEGER)), 0)
		FROM batch_processing
		WHERE batch_id LIKE 'batch_' || TO_CHAR(NOW(), 'YYYYMMDD') || '%'
	`).Scan(&maxNum)

	return fmt.Sprintf("batch_%s_%03d", timestamp, maxNum+1)
}
