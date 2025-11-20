package download

import (
	"context"
	"database/sql"
	"time"

	"go.uber.org/zap"
)

// RecoverCrashedDownloads resets stuck downloads back to PENDING status
func RecoverCrashedDownloads(ctx context.Context, db *sql.DB, logger *zap.Logger) error {
	logger.Info("Starting crash recovery for downloads")

	// Find stuck downloads (DOWNLOADING for > 30 minutes)
	stuckTimeout := 30 * time.Minute

	result, err := db.ExecContext(ctx, `
		UPDATE download_queue
		SET status = 'PENDING',
			last_error = 'Reset by crash recovery (was stuck in DOWNLOADING)',
			download_attempts = download_attempts + 1
		WHERE status = 'DOWNLOADING'
		  AND started_at < NOW() - INTERVAL '30 minutes'
	`)

	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		logger.Info("Recovered stuck downloads",
			zap.Int64("count", rowsAffected),
			zap.Duration("stuck_timeout", stuckTimeout))
	} else {
		logger.Info("No stuck downloads found")
	}

	return nil
}

// RetryFailedDownloads resets failed downloads with retry attempts remaining
func RetryFailedDownloads(ctx context.Context, db *sql.DB, logger *zap.Logger, maxAttempts int) error {
	logger.Info("Checking for failed downloads to retry", zap.Int("max_attempts", maxAttempts))

	result, err := db.ExecContext(ctx, `
		UPDATE download_queue
		SET status = 'PENDING',
			last_error = 'Automatic retry'
		WHERE status = 'FAILED'
		  AND download_attempts < $1
		  AND completed_at > NOW() - INTERVAL '1 hour'
	`, maxAttempts)

	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		logger.Info("Retrying failed downloads",
			zap.Int64("count", rowsAffected))
	}

	return nil
}

// CleanupOldDownloads removes old completed/failed download records
func CleanupOldDownloads(ctx context.Context, db *sql.DB, logger *zap.Logger, retentionDays int) error {
	logger.Info("Cleaning up old download records", zap.Int("retention_days", retentionDays))

	// Delete old DOWNLOADED records (files already processed into batches)
	result, err := db.ExecContext(ctx, `
		DELETE FROM download_queue
		WHERE status = 'DOWNLOADED'
		  AND completed_at < NOW() - INTERVAL '$1 days'
		  AND batch_id IS NOT NULL
	`, retentionDays)

	if err != nil {
		logger.Error("Error cleaning up DOWNLOADED records", zap.Error(err))
	} else {
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected > 0 {
			logger.Info("Cleaned up old DOWNLOADED records", zap.Int64("count", rowsAffected))
		}
	}

	// Delete old FAILED records
	result, err = db.ExecContext(ctx, `
		DELETE FROM download_queue
		WHERE status = 'FAILED'
		  AND completed_at < NOW() - INTERVAL '$1 days'
	`, retentionDays)

	if err != nil {
		logger.Error("Error cleaning up FAILED records", zap.Error(err))
	} else {
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected > 0 {
			logger.Info("Cleaned up old FAILED records", zap.Int64("count", rowsAffected))
		}
	}

	return nil
}
