package telegram

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

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

	text := fmt.Sprintf(`📊 Download Queue Status:

⏳ Pending: %d
⬇️ Downloading: %d
✅ Downloaded: %d
❌ Failed: %d

Total: %d files`, pending, downloading, downloaded, failed, pending+downloading+downloaded+failed)

	r.sendReply(msg.ChatID, text)
}

func (r *Receiver) handleBatches(msg *tgbotapi.Message) {
	rows, err := r.db.Query(`
		SELECT batch_id, status, file_count, created_at
		FROM batch_processing
		WHERE status != 'COMPLETED'
		ORDER BY created_at DESC
		LIMIT 10
	`)

	if err != nil {
		r.sendReply(msg.ChatID, "Error querying batches")
		return
	}
	defer rows.Close()

	text := "📦 Active Batches:\n\n"
	count := 0

	for rows.Next() {
		var batchID, status string
		var fileCount int
		var createdAt string

		rows.Scan(&batchID, &status, &fileCount, &createdAt)
		text += fmt.Sprintf("🆔 %s\n📊 Status: %s\n📁 Files: %d\n⏰ Created: %s\n\n",
			batchID, status, fileCount, createdAt)
		count++
	}

	if count == 0 {
		text = "No active batches"
	}

	r.sendReply(msg.ChatID, text)
}

func (r *Receiver) handleStats(msg *tgbotapi.Message) {
	var totalCompleted, totalFailed int
	r.db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='COMPLETED'").Scan(&totalCompleted)
	r.db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status LIKE 'FAILED%'").Scan(&totalFailed)

	text := fmt.Sprintf(`📈 Processing Statistics (All Time):

✅ Completed Batches: %d
❌ Failed Batches: %d
📊 Success Rate: %.1f%%

⚙️ Architecture: 1 extract + 1 convert + 5 store workers`,
		totalCompleted,
		totalFailed,
		float64(totalCompleted)/float64(totalCompleted+totalFailed+1)*100)

	r.sendReply(msg.ChatID, text)
}

func (r *Receiver) handleHealthCommand(msg *tgbotapi.Message) {
	// Check database
	err := r.db.Ping()
	status := "✅ Healthy"
	if err != nil {
		status = "❌ Unhealthy"
	}

	text := fmt.Sprintf(`🏥 System Health:

Database: %s
Health Endpoint: http://localhost:%d/health
Metrics Endpoint: http://localhost:%d/metrics

Check detailed health: curl http://localhost:%d/health`,
		status, r.cfg.HealthCheckPort, r.cfg.MetricsPort, r.cfg.HealthCheckPort)

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
