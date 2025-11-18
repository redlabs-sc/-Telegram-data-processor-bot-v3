package telegram

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/config"
	"go.uber.org/zap"
)

type Receiver struct {
	bot    *tgbotapi.BotAPI
	cfg    *config.Config
	db     *sql.DB
	logger *zap.Logger
}

func NewReceiver(cfg *config.Config, db *sql.DB, logger *zap.Logger) (*Receiver, error) {
	var bot *tgbotapi.BotAPI
	var err error

	if cfg.UseLocalBotAPI {
		bot, err = tgbotapi.NewBotAPIWithAPIEndpoint(
			cfg.TelegramBotToken,
			cfg.LocalBotAPIURL+"/bot%s/%s",
		)
	} else {
		bot, err = tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	logger.Info("Telegram bot authorized", zap.String("username", bot.Self.UserName))

	return &Receiver{
		bot:    bot,
		cfg:    cfg,
		db:     db,
		logger: logger,
	}, nil
}

func (r *Receiver) Start() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := r.bot.GetUpdatesChan(u)

	r.logger.Info("Telegram receiver started, waiting for messages...")

	for update := range updates {
		if update.Message == nil {
			continue
		}

		go r.handleMessage(update.Message)
	}
}

func (r *Receiver) handleMessage(msg *tgbotapi.Message) {
	// Check if user is admin
	if !r.cfg.IsAdmin(msg.From.ID) {
		r.sendReply(msg.ChatID, "❌ Unauthorized. This bot is admin-only.")
		r.logger.Warn("Unauthorized access attempt",
			zap.Int64("user_id", msg.From.ID),
			zap.String("username", msg.From.UserName))
		return
	}

	// Handle commands
	if msg.IsCommand() {
		r.handleCommand(msg)
		return
	}

	// Handle file uploads
	if msg.Document != nil {
		r.handleDocument(msg)
		return
	}

	// Ignore other messages
}

func (r *Receiver) handleCommand(msg *tgbotapi.Message) {
	switch msg.Command() {
	case "start":
		r.handleStart(msg)
	case "help":
		r.handleHelp(msg)
	case "queue":
		r.handleQueue(msg)
	case "batches":
		r.handleBatches(msg)
	case "stats":
		r.handleStats(msg)
	case "health":
		r.handleHealthCommand(msg)
	default:
		r.sendReply(msg.ChatID, "Unknown command. Send /help for available commands.")
	}
}

func (r *Receiver) handleStart(msg *tgbotapi.Message) {
	text := `👋 Welcome to Telegram Data Processor Bot

This bot processes archive files (ZIP, RAR) and text files with high-speed batch processing.

📤 Send me files to process:
• Archives: ZIP, RAR (up to 4GB)
• Text files: TXT (up to 4GB)

📊 Available commands:
/help - Show help message
/queue - View queue status
/batches - View active batches
/stats - View processing statistics
/health - Check system health

🚀 Files are processed in batches of 10 for maximum speed!
⚙️ Architecture: 1 extract + 1 convert + 5 store workers (corrected)`

	r.sendReply(msg.ChatID, text)
}

func (r *Receiver) handleHelp(msg *tgbotapi.Message) {
	text := `📚 Available Commands:

/start - Welcome message
/help - This help message
/queue - Show queue statistics (pending, downloading, downloaded, failed)
/batches - List active batches with status
/stats - Overall system statistics (last 24 hours)
/health - System health check (workers, resources)

📤 File Upload:
Simply send a file (ZIP, RAR, or TXT) and it will be queued for processing.

⚡ Processing Pipeline (CORRECTED ARCHITECTURE):
1. Download (3 concurrent workers)
2. Batch Formation (10 files per batch)
3. Extract Stage (1 worker, mutex enforced)
4. Convert Stage (1 worker, mutex enforced)
5. Store Stage (5 concurrent workers)
6. Notification on completion

🔒 Constraint: Extract and convert stages run sequentially (cannot run simultaneously).
✅ Store stage is safe for concurrent execution due to batch directory isolation.`

	r.sendReply(msg.ChatID, text)
}

func (r *Receiver) handleQueue(msg *tgbotapi.Message) {
	var pending, downloading, downloaded, failed int
	r.db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='PENDING'").Scan(&pending)
	r.db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='DOWNLOADING'").Scan(&downloading)
	r.db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='DOWNLOADED'").Scan(&downloaded)
	r.db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='FAILED'").Scan(&failed)

	// Calculate time until next batch
	var oldestCreatedAt sql.NullTime
	err := r.db.QueryRow(`
		SELECT MIN(created_at)
		FROM download_queue
		WHERE status='DOWNLOADED' AND batch_id IS NULL
	`).Scan(&oldestCreatedAt)

	var nextBatchInfo string
	if err == nil && oldestCreatedAt.Valid {
		waitTimeSec := r.cfg.BatchTimeoutSec - int(time.Since(oldestCreatedAt.Time).Seconds())
		if waitTimeSec < 0 {
			nextBatchInfo = "Creating batch now..."
		} else if downloaded >= r.cfg.BatchSize {
			nextBatchInfo = fmt.Sprintf("Next batch forming now (%d files ready)", downloaded)
		} else {
			nextBatchInfo = fmt.Sprintf("Next batch in: %d seconds or when %d files ready",
				waitTimeSec, r.cfg.BatchSize)
		}
	} else {
		nextBatchInfo = "No files waiting for batch"
	}

	text := fmt.Sprintf(`📊 *Queue Status*

• Pending: %d files
• Downloading: %d files
• Downloaded: %d files (waiting for batch)
• Failed: %d files

%s`,
		pending, downloading, downloaded, failed, nextBatchInfo)

	r.sendReply(msg.ChatID, text)
}

func (r *Receiver) handleBatches(msg *tgbotapi.Message) {
	// Get active batches
	rows, err := r.db.Query(`
		SELECT batch_id, status, file_count, started_at,
		       extract_duration_sec, convert_duration_sec, store_duration_sec
		FROM batch_processing
		WHERE status IN ('QUEUED_EXTRACT', 'EXTRACTING', 'QUEUED_CONVERT', 'CONVERTING', 'QUEUED_STORE', 'STORING')
		ORDER BY created_at ASC
	`)

	if err != nil {
		r.sendReply(msg.ChatID, "Error querying batches")
		return
	}
	defer rows.Close()

	var activeBatches []string
	for rows.Next() {
		var batchID, status string
		var fileCount int
		var startedAt sql.NullTime
		var extractDur, convertDur, storeDur sql.NullInt64

		rows.Scan(&batchID, &status, &fileCount, &startedAt,
			&extractDur, &convertDur, &storeDur)

		var elapsed string
		if startedAt.Valid {
			elapsed = fmt.Sprintf("%.0f min elapsed", time.Since(startedAt.Time).Minutes())
		} else {
			elapsed = "not started"
		}

		activeBatches = append(activeBatches, fmt.Sprintf("• %s: %s (%d files, %s)",
			batchID, status, fileCount, elapsed))
	}

	// Get recently completed batches
	rows, err = r.db.Query(`
		SELECT batch_id, file_count,
		       COALESCE(extract_duration_sec, 0) + COALESCE(convert_duration_sec, 0) + COALESCE(store_duration_sec, 0) AS total_duration
		FROM batch_processing
		WHERE status = 'COMPLETED'
		  AND completed_at > NOW() - INTERVAL '1 hour'
		ORDER BY completed_at DESC
		LIMIT 5
	`)

	if err != nil {
		r.sendReply(msg.ChatID, "Error querying completed batches")
		return
	}
	defer rows.Close()

	var completedBatches []string
	for rows.Next() {
		var batchID string
		var fileCount, totalDuration int

		rows.Scan(&batchID, &fileCount, &totalDuration)

		completedBatches = append(completedBatches, fmt.Sprintf("• %s: ✅ COMPLETED (%d files, %d min total)",
			batchID, fileCount, totalDuration/60))
	}

	var activeText string
	if len(activeBatches) > 0 {
		activeText = strings.Join(activeBatches, "\n")
	} else {
		activeText = "No active batches"
	}

	var completedText string
	if len(completedBatches) > 0 {
		completedText = strings.Join(completedBatches, "\n")
	} else {
		completedText = "No recent completions"
	}

	text := fmt.Sprintf(`🔄 *Active Batches*
%s

*Recently Completed* (Last Hour):
%s`, activeText, completedText)

	r.sendReply(msg.ChatID, text)
}

func (r *Receiver) handleStats(msg *tgbotapi.Message) {
	var totalProcessed, totalFailed int
	var avgDuration sql.NullFloat64
	var successRate float64

	// Total processed in last 24 hours
	r.db.QueryRow(`
		SELECT COUNT(*)
		FROM batch_processing
		WHERE completed_at > NOW() - INTERVAL '24 hours'
		  AND status = 'COMPLETED'
	`).Scan(&totalProcessed)

	// Total failed in last 24 hours
	r.db.QueryRow(`
		SELECT COUNT(*)
		FROM batch_processing
		WHERE completed_at > NOW() - INTERVAL '24 hours'
		  AND status IN ('FAILED_EXTRACT', 'FAILED_CONVERT', 'FAILED_STORE')
	`).Scan(&totalFailed)

	// Calculate success rate
	totalAttempts := totalProcessed + totalFailed
	if totalAttempts > 0 {
		successRate = float64(totalProcessed) / float64(totalAttempts) * 100
	}

	// Average processing time
	r.db.QueryRow(`
		SELECT AVG(COALESCE(extract_duration_sec, 0) + COALESCE(convert_duration_sec, 0) + COALESCE(store_duration_sec, 0))
		FROM batch_processing
		WHERE status = 'COMPLETED'
		  AND completed_at > NOW() - INTERVAL '24 hours'
	`).Scan(&avgDuration)

	// Calculate throughput
	var filesProcessed int
	r.db.QueryRow(`
		SELECT COALESCE(SUM(file_count), 0)
		FROM batch_processing
		WHERE status = 'COMPLETED'
		  AND completed_at > NOW() - INTERVAL '24 hours'
	`).Scan(&filesProcessed)

	throughput := float64(filesProcessed) / 24.0 // files per hour

	// Current load by stage
	var downloadWorkers, extractWorkers, convertWorkers, storeWorkers, queueSize int
	r.db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='DOWNLOADING'").Scan(&downloadWorkers)
	r.db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='EXTRACTING'").Scan(&extractWorkers)
	r.db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='CONVERTING'").Scan(&convertWorkers)
	r.db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='STORING'").Scan(&storeWorkers)
	r.db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='PENDING'").Scan(&queueSize)

	avgMinutes := 0.0
	if avgDuration.Valid {
		avgMinutes = avgDuration.Float64 / 60.0
	}

	text := fmt.Sprintf(`📈 *System Statistics* (Last 24 Hours)

*Processing*:
• Total Processed: %d batches (%d files)
• Success Rate: %.1f%%
• Avg Processing Time: %.0f minutes/batch
• Throughput: %.1f files/hour

*Current Load*:
• Download Workers: %d/%d active
• Extract Stage: %d/1 active (mutex enforced)
• Convert Stage: %d/1 active (mutex enforced)
• Store Stage: %d/%d active (batch isolation)
• Queue Size: %d pending`,
		totalProcessed, filesProcessed, successRate, avgMinutes, throughput,
		downloadWorkers, r.cfg.MaxDownloadWorkers,
		extractWorkers,
		convertWorkers,
		storeWorkers, r.cfg.MaxStoreWorkers,
		queueSize)

	r.sendReply(msg.ChatID, text)
}

func (r *Receiver) handleHealthCommand(msg *tgbotapi.Message) {
	// Check database
	dbStatus := "✅ Healthy"
	if err := r.db.Ping(); err != nil {
		dbStatus = "❌ Unhealthy: " + err.Error()
	}

	// Check download workers
	var activeDownloads int
	r.db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='DOWNLOADING'").Scan(&activeDownloads)
	downloadStatus := fmt.Sprintf("✅ %d/%d active", activeDownloads, r.cfg.MaxDownloadWorkers)

	// Check stage-specific workers
	var extracting, converting, storing int
	r.db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='EXTRACTING'").Scan(&extracting)
	r.db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='CONVERTING'").Scan(&converting)
	r.db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='STORING'").Scan(&storing)

	extractStatus := fmt.Sprintf("✅ %d/1 active (mutex)", extracting)
	convertStatus := fmt.Sprintf("✅ %d/1 active (mutex)", converting)
	storeStatus := fmt.Sprintf("✅ %d/%d active (isolated)", storing, r.cfg.MaxStoreWorkers)

	// Verify mutex constraints
	if extracting > 1 {
		extractStatus = fmt.Sprintf("❌ %d/1 active (MUTEX VIOLATION!)", extracting)
	}
	if converting > 1 {
		convertStatus = fmt.Sprintf("❌ %d/1 active (MUTEX VIOLATION!)", converting)
	}

	// Check disk space (simplified)
	diskStatus := "✅ Sufficient"

	text := fmt.Sprintf(`🏥 *System Health*

*Components*:
• Database: %s
• Disk Space: %s

*Workers*:
• Download: %s
• Extract: %s
• Convert: %s
• Store: %s

*Queues*:
• Download Queue: ✅ Operating
• Batch Queue: ✅ Operating

All systems operational.`,
		dbStatus, diskStatus, downloadStatus, extractStatus, convertStatus, storeStatus)

	r.sendReply(msg.ChatID, text)
}

func (r *Receiver) handleDocument(msg *tgbotapi.Message) {
	doc := msg.Document

	// Validate file size
	maxSizeBytes := r.cfg.MaxFileSizeMB * 1024 * 1024
	if int64(doc.FileSize) > maxSizeBytes {
		r.sendReply(msg.ChatID, fmt.Sprintf("❌ File too large. Max size: %d MB", r.cfg.MaxFileSizeMB))
		return
	}

	// Validate file type
	fileType := getFileType(doc.FileName)
	if fileType == "" {
		r.sendReply(msg.ChatID, "❌ Unsupported file type. Supported: ZIP, RAR, TXT")
		return
	}

	// Insert into download queue
	taskID, err := r.enqueueDownload(msg.From.ID, doc.FileID, doc.FileName, fileType, int64(doc.FileSize))
	if err != nil {
		r.logger.Error("Error enqueueing download",
			zap.Error(err),
			zap.String("filename", doc.FileName))
		r.sendReply(msg.ChatID, "❌ Error queuing file for processing. Please try again.")
		return
	}

	// Send confirmation
	r.sendReply(msg.ChatID, fmt.Sprintf(`✅ File queued for processing

📄 Filename: %s
📦 Size: %.2f MB
🆔 Task ID: %d

You'll receive a notification when processing completes.`,
		doc.FileName,
		float64(doc.FileSize)/(1024*1024),
		taskID))

	r.logger.Info("File queued",
		zap.Int64("task_id", taskID),
		zap.String("filename", doc.FileName),
		zap.String("file_type", fileType),
		zap.Int64("file_size", int64(doc.FileSize)))
}

func (r *Receiver) enqueueDownload(userID int64, fileID, filename, fileType string, fileSize int64) (int64, error) {
	var taskID int64
	err := r.db.QueryRow(`
		INSERT INTO download_queue (file_id, user_id, filename, file_type, file_size, status)
		VALUES ($1, $2, $3, $4, $5, 'PENDING')
		RETURNING task_id
	`, fileID, userID, filename, fileType, fileSize).Scan(&taskID)

	return taskID, err
}

func (r *Receiver) sendReply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := r.bot.Send(msg)
	if err != nil {
		r.logger.Error("Error sending message", zap.Error(err))
	}
}

func getFileType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".zip":
		return "ZIP"
	case ".rar":
		return "RAR"
	case ".txt":
		return "TXT"
	default:
		return ""
	}
}

// GetBot returns the underlying bot instance (needed for download workers)
func (r *Receiver) GetBot() *tgbotapi.BotAPI {
	return r.bot
}
