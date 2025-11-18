package download

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/config"
	"go.uber.org/zap"
)

type Worker struct {
	id     string
	bot    *tgbotapi.BotAPI
	cfg    *config.Config
	db     *sql.DB
	logger *zap.Logger
}

func NewWorker(id string, bot *tgbotapi.BotAPI, cfg *config.Config, db *sql.DB, logger *zap.Logger) *Worker {
	return &Worker{
		id:     id,
		bot:    bot,
		cfg:    cfg,
		db:     db,
		logger: logger.With(zap.String("worker", id)),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Download worker started")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Download worker stopping")
			return
		case <-ticker.C:
			w.processNext(ctx)
		}
	}
}

func (w *Worker) processNext(ctx context.Context) {
	// Claim next task with optimistic locking
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		w.logger.Error("Error starting transaction", zap.Error(err))
		return
	}
	defer tx.Rollback()

	var task struct {
		TaskID   int64
		FileID   string
		Filename string
		FileType string
		FileSize int64
	}

	err = tx.QueryRowContext(ctx, `
		SELECT task_id, file_id, filename, file_type, file_size
		FROM download_queue
		WHERE status = 'PENDING'
		ORDER BY priority DESC, created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`).Scan(&task.TaskID, &task.FileID, &task.Filename, &task.FileType, &task.FileSize)

	if err == sql.ErrNoRows {
		// No pending tasks
		return
	}
	if err != nil {
		w.logger.Error("Error querying task", zap.Error(err))
		return
	}

	// Mark as DOWNLOADING
	_, err = tx.ExecContext(ctx, `
		UPDATE download_queue
		SET status = 'DOWNLOADING', started_at = NOW()
		WHERE task_id = $1
	`, task.TaskID)

	if err != nil {
		w.logger.Error("Error updating status", zap.Error(err))
		return
	}

	if err := tx.Commit(); err != nil {
		w.logger.Error("Error committing transaction", zap.Error(err))
		return
	}

	w.logger.Info("Claimed task",
		zap.Int64("task_id", task.TaskID),
		zap.String("filename", task.Filename))

	// Download file with timeout
	downloadCtx, cancel := context.WithTimeout(ctx, time.Duration(w.cfg.DownloadTimeoutSec)*time.Second)
	defer cancel()

	err = w.downloadFile(downloadCtx, task.TaskID, task.FileID, task.Filename)

	if err != nil {
		// Mark as FAILED
		w.logger.Error("Download failed",
			zap.Int64("task_id", task.TaskID),
			zap.Error(err))

		w.db.Exec(`
			UPDATE download_queue
			SET status = 'FAILED',
				last_error = $2,
				download_attempts = download_attempts + 1,
				completed_at = NOW()
			WHERE task_id = $1
		`, task.TaskID, err.Error())
	} else {
		// Mark as DOWNLOADED
		w.logger.Info("Download completed",
			zap.Int64("task_id", task.TaskID),
			zap.String("filename", task.Filename))

		w.db.Exec(`
			UPDATE download_queue
			SET status = 'DOWNLOADED',
				completed_at = NOW()
			WHERE task_id = $1
		`, task.TaskID)
	}
}

func (w *Worker) downloadFile(ctx context.Context, taskID int64, fileID, filename string) error {
	// Get file from Telegram
	fileConfig := tgbotapi.FileConfig{FileID: fileID}
	file, err := w.bot.GetFile(fileConfig)
	if err != nil {
		return fmt.Errorf("get file error: %w", err)
	}

	// Determine download URL
	var fileURL string
	if w.cfg.UseLocalBotAPI {
		fileURL = fmt.Sprintf("%s/file/bot%s/%s", w.cfg.LocalBotAPIURL, w.cfg.TelegramBotToken, file.FilePath)
	} else {
		fileURL = file.Link(w.cfg.TelegramBotToken)
	}

	// Download file to temporary location
	tempPath := filepath.Join("downloads", fmt.Sprintf("%d_%s", taskID, filename))

	// Ensure downloads directory exists
	os.MkdirAll("downloads", 0755)

	// Create output file
	out, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("create file error: %w", err)
	}
	defer out.Close()

	// Download with streaming
	req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
	if err != nil {
		return fmt.Errorf("create request error: %w", err)
	}

	client := &http.Client{
		Timeout: time.Duration(w.cfg.DownloadTimeoutSec) * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http get error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http status: %d", resp.StatusCode)
	}

	// Compute SHA256 while downloading
	hash := sha256.New()
	multiWriter := io.MultiWriter(out, hash)

	_, err = io.Copy(multiWriter, resp.Body)
	if err != nil {
		return fmt.Errorf("copy error: %w", err)
	}

	// Store hash in database
	sha256Hash := hex.EncodeToString(hash.Sum(nil))
	_, err = w.db.Exec(`
		UPDATE download_queue
		SET sha256_hash = $2
		WHERE task_id = $1
	`, taskID, sha256Hash)

	if err != nil {
		w.logger.Warn("Error storing hash", zap.Error(err))
	}

	w.logger.Info("File downloaded",
		zap.Int64("task_id", taskID),
		zap.String("path", tempPath),
		zap.String("sha256", sha256Hash))

	return nil
}
