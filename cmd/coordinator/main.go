package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/lib/pq"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/config"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/internal/health"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/internal/logger"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/internal/metrics"
	"go.uber.org/zap"
)

func main() {
	// 1. Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// 2. Initialize logger
	log, err := logger.InitLogger(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()

	log.Info("Starting Telegram Bot - Batch-Based Parallel Processing System")
	log.Info("Architecture: 1 extract worker + 1 convert worker + 5 store workers (corrected)")

	// 3. Connect to database
	db, err := sql.Open("postgres", cfg.GetDatabaseDSN())
	if err != nil {
		log.Fatal("Error connecting to database", zap.Error(err))
	}
	defer db.Close()

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	// Test database connection
	if err := db.Ping(); err != nil {
		log.Fatal("Error pinging database", zap.Error(err))
	}
	log.Info("Connected to database successfully",
		zap.String("host", cfg.DBHost),
		zap.Int("port", cfg.DBPort),
		zap.String("database", cfg.DBName))

	// 4. Start health check server
	health.StartHealthServer(cfg, db, log)
	log.Info("Health check server started", zap.Int("port", cfg.HealthCheckPort))

	// 5. Start metrics server
	metrics.StartMetricsServer(cfg, db, log)
	log.Info("Metrics server started", zap.Int("port", cfg.MetricsPort))

	// 6. TODO: Initialize Telegram bot receiver (Phase 2)
	// 7. TODO: Start download workers (Phase 2)
	// 8. TODO: Start batch coordinator (Phase 3)
	// 9. TODO: Start stage workers - extract, convert, store (Phase 4)

	log.Info("Phase 1 foundation completed successfully",
		zap.Int("max_extract_workers", cfg.MaxExtractWorkers),
		zap.Int("max_convert_workers", cfg.MaxConvertWorkers),
		zap.Int("max_store_workers", cfg.MaxStoreWorkers))

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Info("All services started successfully - waiting for shutdown signal")
	sig := <-sigChan
	log.Info("Received shutdown signal", zap.String("signal", sig.String()))

	// TODO: Graceful shutdown logic
	log.Info("Shutting down gracefully...")
	log.Info("Shutdown complete")
}
