package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	// Telegram
	TelegramBotToken string
	AdminIDs         []int64
	UseLocalBotAPI   bool
	LocalBotAPIURL   string
	MaxFileSizeMB    int64

	// Database
	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string
	DBSSLMode  string

	// Workers - CORRECTED ARCHITECTURE
	MaxDownloadWorkers int
	MaxExtractWorkers  int // Must be 1 (mutex enforced)
	MaxConvertWorkers  int // Must be 1 (mutex enforced)
	MaxStoreWorkers    int // Safe for concurrency (batch isolation)
	BatchSize          int
	BatchTimeoutSec    int

	// Timeouts
	DownloadTimeoutSec int
	ExtractTimeoutSec  int
	ConvertTimeoutSec  int
	StoreTimeoutSec    int

	// Resource Limits
	MaxRAMPercent int
	MaxCPUPercent int

	// Logging
	LogLevel  string
	LogFormat string
	LogFile   string

	// Cleanup
	CompletedBatchRetentionHours int
	FailedBatchRetentionDays     int

	// Monitoring
	MetricsPort     int
	HealthCheckPort int
}

func LoadConfig() (*Config, error) {
	// Load .env file (ignore error if file doesn't exist)
	_ = godotenv.Load()

	cfg := &Config{}

	// Parse Telegram config
	cfg.TelegramBotToken = getEnv("TELEGRAM_BOT_TOKEN", "")
	if cfg.TelegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}

	adminIDsStr := getEnv("ADMIN_IDS", "")
	if adminIDsStr == "" {
		return nil, fmt.Errorf("ADMIN_IDS is required")
	}
	cfg.AdminIDs = parseAdminIDs(adminIDsStr)

	cfg.UseLocalBotAPI = getEnvBool("USE_LOCAL_BOT_API", true)
	cfg.LocalBotAPIURL = getEnv("LOCAL_BOT_API_URL", "http://localhost:8081")
	cfg.MaxFileSizeMB = getEnvInt64("MAX_FILE_SIZE_MB", 4096)

	// Parse Database config
	cfg.DBHost = getEnv("DB_HOST", "localhost")
	cfg.DBPort = getEnvInt("DB_PORT", 5432)
	cfg.DBName = getEnv("DB_NAME", "telegram_bot_option2")
	cfg.DBUser = getEnv("DB_USER", "bot_user")
	cfg.DBPassword = getEnv("DB_PASSWORD", "")
	if cfg.DBPassword == "" {
		return nil, fmt.Errorf("DB_PASSWORD is required")
	}
	cfg.DBSSLMode = getEnv("DB_SSL_MODE", "disable")

	// Parse Worker config
	cfg.MaxDownloadWorkers = getEnvInt("MAX_DOWNLOAD_WORKERS", 3)
	cfg.MaxExtractWorkers = getEnvInt("MAX_EXTRACT_WORKERS", 1)
	cfg.MaxConvertWorkers = getEnvInt("MAX_CONVERT_WORKERS", 1)
	cfg.MaxStoreWorkers = getEnvInt("MAX_STORE_WORKERS", 5)
	cfg.BatchSize = getEnvInt("BATCH_SIZE", 10)
	cfg.BatchTimeoutSec = getEnvInt("BATCH_TIMEOUT_SEC", 300)

	// CRITICAL: Validate worker constraints
	if cfg.MaxExtractWorkers != 1 {
		return nil, fmt.Errorf("MAX_EXTRACT_WORKERS must be 1 (architectural constraint: extract cannot run concurrently)")
	}
	if cfg.MaxConvertWorkers != 1 {
		return nil, fmt.Errorf("MAX_CONVERT_WORKERS must be 1 (architectural constraint: convert cannot run concurrently)")
	}

	// Parse Timeout config
	cfg.DownloadTimeoutSec = getEnvInt("DOWNLOAD_TIMEOUT_SEC", 1800)
	cfg.ExtractTimeoutSec = getEnvInt("EXTRACT_TIMEOUT_SEC", 1800)
	cfg.ConvertTimeoutSec = getEnvInt("CONVERT_TIMEOUT_SEC", 1800)
	cfg.StoreTimeoutSec = getEnvInt("STORE_TIMEOUT_SEC", 3600)

	// Parse Resource Limits
	cfg.MaxRAMPercent = getEnvInt("MAX_RAM_PERCENT", 20)
	cfg.MaxCPUPercent = getEnvInt("MAX_CPU_PERCENT", 50)

	// Parse Logging config
	cfg.LogLevel = getEnv("LOG_LEVEL", "info")
	cfg.LogFormat = getEnv("LOG_FORMAT", "json")
	cfg.LogFile = getEnv("LOG_FILE", "logs/coordinator.log")

	// Parse Cleanup config
	cfg.CompletedBatchRetentionHours = getEnvInt("COMPLETED_BATCH_RETENTION_HOURS", 1)
	cfg.FailedBatchRetentionDays = getEnvInt("FAILED_BATCH_RETENTION_DAYS", 7)

	// Parse Monitoring config
	cfg.MetricsPort = getEnvInt("METRICS_PORT", 9090)
	cfg.HealthCheckPort = getEnvInt("HEALTH_CHECK_PORT", 8080)

	return cfg, nil
}

// IsAdmin checks if a user ID is in the admin list
func (c *Config) IsAdmin(userID int64) bool {
	for _, adminID := range c.AdminIDs {
		if adminID == userID {
			return true
		}
	}
	return false
}

// GetDatabaseDSN returns the PostgreSQL connection string
func (c *Config) GetDatabaseDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode)
}

// Helper functions
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}

func parseAdminIDs(input string) []int64 {
	parts := strings.Split(input, ",")
	ids := make([]int64, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}

	return ids
}
