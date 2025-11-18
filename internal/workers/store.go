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

type StoreWorker struct {
	id     string
	cfg    *config.Config
	db     *sql.DB
	logger *zap.Logger
}

func NewStoreWorker(id string, cfg *config.Config, db *sql.DB, logger *zap.Logger) *StoreWorker {
	return &StoreWorker{
		id:     id,
		cfg:    cfg,
		db:     db,
		logger: logger.With(zap.String("worker", id)),
	}
}

func (sw *StoreWorker) Start(ctx context.Context) {
	sw.logger.Info("Store worker started (5 instances safe - batch directory isolation)")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			sw.logger.Info("Store worker stopping")
			return
		case <-ticker.C:
			sw.processNext(ctx)
		}
	}
}

func (sw *StoreWorker) processNext(ctx context.Context) {
	// NO MUTEX NEEDED: Each batch has isolated directories
	// Safe for concurrent execution across different batches
	// Up to 5 store workers can run simultaneously

	// Claim next batch ready for storing
	tx, err := sw.db.BeginTx(ctx, nil)
	if err != nil {
		sw.logger.Error("Error starting transaction", zap.Error(err))
		return
	}
	defer tx.Rollback()

	var batchID string

	err = tx.QueryRowContext(ctx, `
		SELECT batch_id
		FROM batch_processing
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`, StatusQueuedStore).Scan(&batchID)

	if err == sql.ErrNoRows {
		// No batches ready for storing
		return
	}
	if err != nil {
		sw.logger.Error("Error querying batch", zap.Error(err))
		return
	}

	// Mark as STORING
	_, err = tx.ExecContext(ctx, `
		UPDATE batch_processing
		SET status = $2
		WHERE batch_id = $1
	`, batchID, StatusStoring)

	if err != nil {
		sw.logger.Error("Error updating batch status", zap.Error(err))
		return
	}

	if err := tx.Commit(); err != nil {
		sw.logger.Error("Error committing transaction", zap.Error(err))
		return
	}

	sw.logger.Info("Processing store stage", zap.String("batch_id", batchID))

	// Run store stage
	if err := sw.runStoreStage(ctx, batchID); err != nil {
		sw.logger.Error("Store failed", zap.String("batch_id", batchID), zap.Error(err))
		sw.db.Exec(`
			UPDATE batch_processing
			SET status=$2,
			    last_error=$3,
			    completed_at=NOW()
			WHERE batch_id=$1
		`, batchID, StatusFailedStore, err.Error())
	} else {
		// Mark as COMPLETED
		sw.db.Exec(`UPDATE batch_processing SET status=$2, completed_at=NOW() WHERE batch_id=$1`,
			batchID, StatusCompleted)
		sw.logger.Info("Store completed - batch finished", zap.String("batch_id", batchID))
	}
}

func (sw *StoreWorker) runStoreStage(ctx context.Context, batchID string) error {
	batchRoot := filepath.Join("batches", batchID)

	// Save current working directory
	originalWD, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	defer os.Chdir(originalWD)

	// Change to batch directory
	// CRITICAL: This makes relative paths in store.go work correctly
	// Each batch has its own isolated directory, enabling safe concurrent execution
	if err := os.Chdir(batchRoot); err != nil {
		return fmt.Errorf("change to batch directory: %w", err)
	}

	startTime := time.Now()

	// Build path to store.go (absolute path to preserved code)
	storePath := filepath.Join(originalWD, "app", "extraction", "store.go")

	// Create context with timeout
	storeCtx, cancel := context.WithTimeout(ctx, time.Duration(sw.cfg.StoreTimeoutSec)*time.Second)
	defer cancel()

	// Execute store.go as subprocess
	// Store.go reads from app/extraction/files/all_extracted.txt and stores to database
	// SAFE for concurrent execution: Each batch has isolated directories
	// - batch_001/app/extraction/files/all_extracted.txt
	// - batch_002/app/extraction/files/all_extracted.txt
	// No file conflicts! Database UNIQUE constraint handles duplicate inserts.
	cmd := exec.CommandContext(storeCtx, "go", "run", storePath)
	output, err := cmd.CombinedOutput()

	// Log output to batch-specific log file
	logPath := filepath.Join("logs", "store.log")
	os.MkdirAll("logs", 0755)
	os.WriteFile(logPath, output, 0644)

	duration := time.Since(startTime)

	if err != nil {
		sw.logger.Error("Store stage failed",
			zap.String("batch_id", batchID),
			zap.Duration("duration", duration),
			zap.Error(err))
		return fmt.Errorf("store stage failed: %w", err)
	}

	// Store duration in database
	sw.db.Exec(`
		UPDATE batch_processing
		SET store_duration_sec = $2
		WHERE batch_id = $1
	`, batchID, int(duration.Seconds()))

	sw.logger.Info("Store stage completed",
		zap.String("batch_id", batchID),
		zap.Duration("duration", duration))

	return nil
}
