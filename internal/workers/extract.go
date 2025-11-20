package workers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/redlabs-sc/telegram-data-processor-bot-v3/config"
	"go.uber.org/zap"
)

type ExtractWorker struct {
	id     string
	cfg    *config.Config
	db     *sql.DB
	logger *zap.Logger
}

func NewExtractWorker(id string, cfg *config.Config, db *sql.DB, logger *zap.Logger) *ExtractWorker {
	return &ExtractWorker{
		id:     id,
		cfg:    cfg,
		db:     db,
		logger: logger.With(zap.String("worker", id)),
	}
}

func (ew *ExtractWorker) Start(ctx context.Context) {
	ew.logger.Info("Extract worker started (CRITICAL: Only 1 instance allowed, mutex enforced)")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			ew.logger.Info("Extract worker stopping")
			return
		case <-ticker.C:
			ew.processNext(ctx)
		}
	}
}

func (ew *ExtractWorker) processNext(ctx context.Context) {
	// CRITICAL: Acquire global extract mutex
	// Only ONE extract operation can run at a time across ALL batches
	ExtractMutex.Lock()
	defer ExtractMutex.Unlock()

	ew.logger.Debug("Acquired extract mutex, claiming batch")

	// Claim next queued batch for extraction
	tx, err := ew.db.BeginTx(ctx, nil)
	if err != nil {
		ew.logger.Error("Error starting transaction", zap.Error(err))
		return
	}
	defer tx.Rollback()

	var batchID string
	var fileCount int

	err = tx.QueryRowContext(ctx, `
		SELECT batch_id, file_count
		FROM batch_processing
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`, StatusQueuedExtract).Scan(&batchID, &fileCount)

	if err == sql.ErrNoRows {
		// No batches ready for extraction
		return
	}
	if err != nil {
		ew.logger.Error("Error querying batch", zap.Error(err))
		return
	}

	// Mark as EXTRACTING
	_, err = tx.ExecContext(ctx, `
		UPDATE batch_processing
		SET status = $2,
		    started_at = NOW()
		WHERE batch_id = $1
	`, batchID, StatusExtracting)

	if err != nil {
		ew.logger.Error("Error updating batch status", zap.Error(err))
		return
	}

	if err := tx.Commit(); err != nil {
		ew.logger.Error("Error committing transaction", zap.Error(err))
		return
	}

	ew.logger.Info("Processing extract stage",
		zap.String("batch_id", batchID),
		zap.Int("file_count", fileCount))

	// Run extract stage
	if err := ew.runExtractStage(ctx, batchID); err != nil {
		ew.logger.Error("Extract failed", zap.String("batch_id", batchID), zap.Error(err))
		ew.db.Exec(`
			UPDATE batch_processing
			SET status=$2,
			    last_error=$3,
			    completed_at=NOW()
			WHERE batch_id=$1
		`, batchID, StatusFailedExtract, err.Error())
	} else {
		// Move to QUEUED_CONVERT status (for convert worker to pick up)
		ew.db.Exec(`UPDATE batch_processing SET status=$2 WHERE batch_id=$1`,
			batchID, StatusQueuedConvert)
		ew.logger.Info("Extract completed", zap.String("batch_id", batchID))
	}
}

func (ew *ExtractWorker) runExtractStage(ctx context.Context, batchID string) error {
	batchRoot := filepath.Join("batches", batchID)

	// Save current working directory
	originalWD, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	defer os.Chdir(originalWD) // Always restore

	// Change to batch directory
	// CRITICAL: This makes relative paths in extract.go work correctly
	if err := os.Chdir(batchRoot); err != nil {
		return fmt.Errorf("change to batch directory: %w", err)
	}

	startTime := time.Now()

	// Build path to extract.go (absolute path to preserved code)
	// Extract.go expects to run from batch root and process files in downloads/
	extractPath := filepath.Join(originalWD, "app", "extraction", "extract", "extract.go")

	// Create context with timeout
	extractCtx, cancel := context.WithTimeout(ctx, time.Duration(ew.cfg.ExtractTimeoutSec)*time.Second)
	defer cancel()

	// Execute extract.go as subprocess
	// CRITICAL: Working directory is batch root, so extract.go processes
	// files in downloads/ and outputs to app/extraction/files/pass/
	cmd := exec.CommandContext(extractCtx, "go", "run", extractPath)
	output, err := cmd.CombinedOutput()

	// Log output to batch-specific log file
	logPath := filepath.Join("logs", "extract.log")
	os.MkdirAll("logs", 0755)
	os.WriteFile(logPath, output, 0644)

	duration := time.Since(startTime)

	if err != nil {
		ew.logger.Error("Extract stage failed",
			zap.String("batch_id", batchID),
			zap.Duration("duration", duration),
			zap.Error(err))
		return fmt.Errorf("extract stage failed: %w", err)
	}

	// Store duration in database
	ew.db.Exec(`
		UPDATE batch_processing
		SET extract_duration_sec = $2
		WHERE batch_id = $1
	`, batchID, int(duration.Seconds()))

	ew.logger.Info("Extract stage completed",
		zap.String("batch_id", batchID),
		zap.Duration("duration", duration))

	return nil
}
