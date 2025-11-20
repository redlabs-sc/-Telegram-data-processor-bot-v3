package logger

import (
	"os"

	"github.com/redlabs-sc/telegram-data-processor-bot-v3/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// InitLogger creates a new zap logger based on configuration
func InitLogger(cfg *config.Config) (*zap.Logger, error) {
	// Configure log level
	var level zapcore.Level
	switch cfg.LogLevel {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	default:
		level = zapcore.InfoLevel
	}

	// Configure encoding
	var encoding string
	if cfg.LogFormat == "json" {
		encoding = "json"
	} else {
		encoding = "console"
	}

	// Ensure log directory exists
	if err := os.MkdirAll("logs", 0755); err != nil {
		return nil, err
	}

	// Build configuration
	zapConfig := zap.Config{
		Level:            zap.NewAtomicLevelAt(level),
		Development:      false,
		Encoding:         encoding,
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout", cfg.LogFile},
		ErrorOutputPaths: []string{"stderr"},
	}

	// Customize time encoding
	zapConfig.EncoderConfig.TimeKey = "timestamp"
	zapConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	zapConfig.EncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	// Build logger
	logger, err := zapConfig.Build()
	if err != nil {
		return nil, err
	}

	return logger, nil
}
