package batch

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	"github.com/redlabs-sc/telegram-data-processor-bot-v3/config"
	"go.uber.org/zap"
)

type Cleanup struct {
	cfg    *config.Config
	db     *sql.DB
	logger *zap.Logger
}

func NewCleanup(cfg *config.Config, db *sql.DB, logger *zap.Logger) *Cleanup {
	return &Cleanup{
		cfg:    cfg,
		db:     db,
		logger: logger.With(zap.String("component", "batch_cleanup")),
	}
}

func (bc *Cleanup) Start(ctx context.Context) {
	bc.logger.Info("Batch cleanup service started",
		zap.Int("completed_retention_hours", bc.cfg.CompletedBatchRetentionHours),
		zap.Int("failed_retention_days", bc.cfg.FailedBatchRetentionDays))

	// Run cleanup immediately on startup
	bc.cleanupCompletedBatches(ctx)
	bc.archiveFailedBatches(ctx)

	// Then run every 15 minutes
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			bc.logger.Info("Batch cleanup service stopping")
			return
		case <-ticker.C:
			bc.cleanupCompletedBatches(ctx)
			bc.archiveFailedBatches(ctx)
		}
	}
}

func (bc *Cleanup) cleanupCompletedBatches(ctx context.Context) {
	rows, err := bc.db.QueryContext(ctx, `
		SELECT batch_id
		FROM batch_processing
		WHERE status = 'COMPLETED'
		  AND completed_at < NOW() - INTERVAL '1 hour' * $1
	`, bc.cfg.CompletedBatchRetentionHours)

	if err != nil {
		bc.logger.Error("Error querying completed batches", zap.Error(err))
		return
	}
	defer rows.Close()

	cleanedCount := 0
	for rows.Next() {
		var batchID string
		if err := rows.Scan(&batchID); err != nil {
			bc.logger.Error("Error scanning batch_id", zap.Error(err))
			continue
		}

		// Delete batch directory
		batchPath := filepath.Join("batches", batchID)
		if err := os.RemoveAll(batchPath); err != nil {
			bc.logger.Error("Error removing batch directory",
				zap.String("batch_id", batchID),
				zap.Error(err))
		} else {
			bc.logger.Info("Cleaned up completed batch",
				zap.String("batch_id", batchID))
			cleanedCount++
		}
	}

	if cleanedCount > 0 {
		bc.logger.Info("Cleanup completed batches finished",
			zap.Int("cleaned_count", cleanedCount))
	}
}

func (bc *Cleanup) archiveFailedBatches(ctx context.Context) {
	rows, err := bc.db.QueryContext(ctx, `
		SELECT batch_id
		FROM batch_processing
		WHERE status IN ('FAILED_EXTRACT', 'FAILED_CONVERT', 'FAILED_STORE')
		  AND completed_at < NOW() - INTERVAL '1 day' * $1
	`, bc.cfg.FailedBatchRetentionDays)

	if err != nil {
		bc.logger.Error("Error querying failed batches", zap.Error(err))
		return
	}
	defer rows.Close()

	archivedCount := 0
	for rows.Next() {
		var batchID string
		if err := rows.Scan(&batchID); err != nil {
			bc.logger.Error("Error scanning batch_id", zap.Error(err))
			continue
		}

		// Create archive directory if it doesn't exist
		archiveDir := filepath.Join("archive", "failed")
		if err := os.MkdirAll(archiveDir, 0755); err != nil {
			bc.logger.Error("Error creating archive directory",
				zap.String("directory", archiveDir),
				zap.Error(err))
			continue
		}

		// Move to archive
		sourcePath := filepath.Join("batches", batchID)
		destPath := filepath.Join(archiveDir, batchID)

		if err := os.Rename(sourcePath, destPath); err != nil {
			bc.logger.Error("Error archiving failed batch",
				zap.String("batch_id", batchID),
				zap.Error(err))
		} else {
			bc.logger.Info("Archived failed batch",
				zap.String("batch_id", batchID),
				zap.String("archive_path", destPath))
			archivedCount++
		}
	}

	if archivedCount > 0 {
		bc.logger.Info("Archive failed batches finished",
			zap.Int("archived_count", archivedCount))
	}
}
