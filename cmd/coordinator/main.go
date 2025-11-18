package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	_ "github.com/lib/pq"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/config"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/internal/batch"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/internal/download"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/internal/health"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/internal/logger"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/internal/metrics"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/internal/telegram"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/internal/workers"
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

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 6. Crash recovery for downloads
	if err := download.RecoverCrashedDownloads(ctx, db, log); err != nil {
		log.Error("Error during crash recovery", zap.Error(err))
	}

	// 7. Initialize Telegram bot receiver
	receiver, err := telegram.NewReceiver(cfg, db, log)
	if err != nil {
		log.Fatal("Error creating Telegram receiver", zap.Error(err))
	}
	log.Info("Telegram receiver initialized")

	// Start receiver in background
	go receiver.Start()
	log.Info("Telegram receiver started")

	// 8. Start download workers (3 concurrent)
	var wg sync.WaitGroup
	for i := 1; i <= cfg.MaxDownloadWorkers; i++ {
		workerID := fmt.Sprintf("download_worker_%d", i)
		worker := download.NewWorker(workerID, receiver.GetBot(), cfg, db, log)

		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Start(ctx)
		}()

		log.Info("Download worker started", zap.String("worker_id", workerID))
	}

	// 9. Start batch coordinator
	batchCoordinator := batch.NewCoordinator(cfg, db, log)
	wg.Add(1)
	go func() {
		defer wg.Done()
		batchCoordinator.Start(ctx)
	}()
	log.Info("Batch coordinator started",
		zap.Int("batch_size", cfg.BatchSize),
		zap.Int("batch_timeout_sec", cfg.BatchTimeoutSec))

	// 10. Start EXTRACT workers (exactly 1, with global mutex)
	for i := 1; i <= cfg.MaxExtractWorkers; i++ {
		workerID := fmt.Sprintf("extract_worker_%d", i)
		worker := workers.NewExtractWorker(workerID, cfg, db, log)

		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Start(ctx)
		}()

		log.Info("Extract worker started",
			zap.String("worker_id", workerID),
			zap.String("note", "mutex enforced - only 1 batch extracts at a time"))
	}

	// 11. Start CONVERT workers (exactly 1, with global mutex)
	for i := 1; i <= cfg.MaxConvertWorkers; i++ {
		workerID := fmt.Sprintf("convert_worker_%d", i)
		worker := workers.NewConvertWorker(workerID, cfg, db, log)

		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Start(ctx)
		}()

		log.Info("Convert worker started",
			zap.String("worker_id", workerID),
			zap.String("note", "mutex enforced - only 1 batch converts at a time"))
	}

	// 12. Start STORE workers (5 concurrent, batch isolation ensures safety)
	for i := 1; i <= cfg.MaxStoreWorkers; i++ {
		workerID := fmt.Sprintf("store_worker_%d", i)
		worker := workers.NewStoreWorker(workerID, cfg, db, log)

		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Start(ctx)
		}()

		log.Info("Store worker started",
			zap.String("worker_id", workerID),
			zap.String("note", "concurrent safe - batch directory isolation"))
	}

	log.Info("Phase 4 stage workers completed successfully",
		zap.Int("download_workers", cfg.MaxDownloadWorkers),
		zap.Int("batch_size", cfg.BatchSize),
		zap.Int("extract_workers", cfg.MaxExtractWorkers),
		zap.Int("convert_workers", cfg.MaxConvertWorkers),
		zap.Int("store_workers", cfg.MaxStoreWorkers))

	// 13. Start batch cleanup service
	batchCleanup := batch.NewCleanup(cfg, db, log)
	wg.Add(1)
	go func() {
		defer wg.Done()
		batchCleanup.Start(ctx)
	}()
	log.Info("Batch cleanup service started",
		zap.Int("completed_retention_hours", cfg.CompletedBatchRetentionHours),
		zap.Int("failed_retention_days", cfg.FailedBatchRetentionDays))

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Info("All services started successfully - waiting for shutdown signal")
	sig := <-sigChan
	log.Info("Received shutdown signal", zap.String("signal", sig.String()))

	// Graceful shutdown
	log.Info("Shutting down gracefully...")
	cancel() // Stop all workers

	// Wait for workers to finish (with timeout)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Info("All workers stopped gracefully")
	case <-sigChan:
		log.Warn("Forced shutdown - workers may not have stopped cleanly")
	}

	log.Info("Shutdown complete")
}
