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

type ConvertWorker struct {
	id     string
	cfg    *config.Config
	db     *sql.DB
	logger *zap.Logger
}

func NewConvertWorker(id string, cfg *config.Config, db *sql.DB, logger *zap.Logger) *ConvertWorker {
	return &ConvertWorker{
		id:     id,
		cfg:    cfg,
		db:     db,
		logger: logger.With(zap.String("worker", id)),
	}
}

func (cw *ConvertWorker) Start(ctx context.Context) {
	cw.logger.Info("Convert worker started (CRITICAL: Only 1 instance allowed, mutex enforced)")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			cw.logger.Info("Convert worker stopping")
			return
		case <-ticker.C:
			cw.processNext(ctx)
		}
	}
}

func (cw *ConvertWorker) processNext(ctx context.Context) {
	// CRITICAL: Acquire global convert mutex
	// Only ONE convert operation can run at a time across ALL batches
	ConvertMutex.Lock()
	defer ConvertMutex.Unlock()

	cw.logger.Debug("Acquired convert mutex, claiming batch")

	// Claim next batch ready for converting
	tx, err := cw.db.BeginTx(ctx, nil)
	if err != nil {
		cw.logger.Error("Error starting transaction", zap.Error(err))
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
	`, StatusQueuedConvert).Scan(&batchID)

	if err == sql.ErrNoRows {
		// No batches ready for convert
		return
	}
	if err != nil {
		cw.logger.Error("Error querying batch", zap.Error(err))
		return
	}

	// Mark as CONVERTING
	_, err = tx.ExecContext(ctx, `
		UPDATE batch_processing
		SET status = $2
		WHERE batch_id = $1
	`, batchID, StatusConverting)

	if err != nil {
		cw.logger.Error("Error updating batch status", zap.Error(err))
		return
	}

	if err := tx.Commit(); err != nil {
		cw.logger.Error("Error committing transaction", zap.Error(err))
		return
	}

	cw.logger.Info("Processing convert stage", zap.String("batch_id", batchID))

	// Run convert stage
	if err := cw.runConvertStage(ctx, batchID); err != nil {
		cw.logger.Error("Convert failed", zap.String("batch_id", batchID), zap.Error(err))
		cw.db.Exec(`
			UPDATE batch_processing
			SET status=$2,
			    last_error=$3,
			    completed_at=NOW()
			WHERE batch_id=$1
		`, batchID, StatusFailedConvert, err.Error())
	} else {
		// Move to QUEUED_STORE status (for store workers to pick up)
		cw.db.Exec(`UPDATE batch_processing SET status=$2 WHERE batch_id=$1`,
			batchID, StatusQueuedStore)
		cw.logger.Info("Convert completed", zap.String("batch_id", batchID))
	}
}

func (cw *ConvertWorker) runConvertStage(ctx context.Context, batchID string) error {
	batchRoot := filepath.Join("batches", batchID)

	// Save current working directory
	originalWD, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	defer os.Chdir(originalWD)

	// Change to batch directory
	// CRITICAL: This makes relative paths in convert.go work correctly
	if err := os.Chdir(batchRoot); err != nil {
		return fmt.Errorf("change to batch directory: %w", err)
	}

	startTime := time.Now()

	// Build path to convert.go (absolute path to preserved code)
	convertPath := filepath.Join(originalWD, "app", "extraction", "convert", "convert.go")

	// Create context with timeout
	convertCtx, cancel := context.WithTimeout(ctx, time.Duration(cw.cfg.ConvertTimeoutSec)*time.Second)
	defer cancel()

	// Execute convert.go as subprocess
	// Convert.go reads from app/extraction/files/pass/ and outputs to app/extraction/files/txt/
	cmd := exec.CommandContext(convertCtx, "go", "run", convertPath)

	// Set environment variables for convert.go
	// Generate unique output filename with batch ID and timestamp
	outputFileName := fmt.Sprintf("output_%s_%s.txt", batchID, time.Now().Format("20060102_150405"))
	cmd.Env = append(os.Environ(),
		"CONVERT_INPUT_DIR=app/extraction/files/pass",
		fmt.Sprintf("CONVERT_OUTPUT_FILE=app/extraction/files/txt/%s", outputFileName),
	)

	output, err := cmd.CombinedOutput()

	// Log output to batch-specific log file
	logPath := filepath.Join("logs", "convert.log")
	os.MkdirAll("logs", 0755)
	os.WriteFile(logPath, output, 0644)

	duration := time.Since(startTime)

	if err != nil {
		cw.logger.Error("Convert stage failed",
			zap.String("batch_id", batchID),
			zap.Duration("duration", duration),
			zap.Error(err))
		return fmt.Errorf("convert stage failed: %w", err)
	}

	// Store duration in database
	cw.db.Exec(`
		UPDATE batch_processing
		SET convert_duration_sec = $2
		WHERE batch_id = $1
	`, batchID, int(duration.Seconds()))

	cw.logger.Info("Convert stage completed",
		zap.String("batch_id", batchID),
		zap.Duration("duration", duration))

	return nil
}
