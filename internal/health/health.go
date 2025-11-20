package health

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/redlabs-sc/telegram-data-processor-bot-v3/config"
	"go.uber.org/zap"
)

type HealthResponse struct {
	Status     string                 `json:"status"`
	Timestamp  string                 `json:"timestamp"`
	Components map[string]interface{} `json:"components"`
	Queue      map[string]int         `json:"queue"`
	Batches    map[string]int         `json:"batches"`
}

// StartHealthServer starts the health check HTTP server
func StartHealthServer(cfg *config.Config, db *sql.DB, logger *zap.Logger) {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		health := checkHealth(db, logger)

		w.Header().Set("Content-Type", "application/json")
		if health.Status == "healthy" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		json.NewEncoder(w).Encode(health)
	})

	http.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		// Readiness check - can we process requests?
		if err := db.Ping(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ready"))
	})

	http.HandleFunc("/health/live", func(w http.ResponseWriter, r *http.Request) {
		// Liveness check - is the process alive?
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("alive"))
	})

	addr := fmt.Sprintf(":%d", cfg.HealthCheckPort)
	logger.Info("Starting health check server", zap.String("addr", addr))

	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			logger.Error("Health server error", zap.Error(err))
		}
	}()
}

func checkHealth(db *sql.DB, logger *zap.Logger) HealthResponse {
	health := HealthResponse{
		Status:     "healthy",
		Timestamp:  time.Now().Format(time.RFC3339),
		Components: make(map[string]interface{}),
		Queue:      make(map[string]int),
		Batches:    make(map[string]int),
	}

	// Check database
	if err := db.Ping(); err != nil {
		health.Status = "unhealthy"
		health.Components["database"] = map[string]string{
			"status": "unhealthy",
			"error":  err.Error(),
		}
		logger.Warn("Database health check failed", zap.Error(err))
	} else {
		health.Components["database"] = "healthy"
	}

	// Check queue statistics
	var pending, downloading, downloaded, failed int
	db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='PENDING'").Scan(&pending)
	db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='DOWNLOADING'").Scan(&downloading)
	db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='DOWNLOADED'").Scan(&downloaded)
	db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='FAILED'").Scan(&failed)

	health.Queue["pending"] = pending
	health.Queue["downloading"] = downloading
	health.Queue["downloaded"] = downloaded
	health.Queue["failed"] = failed

	// Check batch statistics - using corrected status values
	var queuedExtract, extracting, queuedConvert, converting, queuedStore, storing, completed int
	db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='QUEUED_EXTRACT'").Scan(&queuedExtract)
	db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='EXTRACTING'").Scan(&extracting)
	db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='QUEUED_CONVERT'").Scan(&queuedConvert)
	db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='CONVERTING'").Scan(&converting)
	db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='QUEUED_STORE'").Scan(&queuedStore)
	db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='STORING'").Scan(&storing)
	db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='COMPLETED' AND completed_at > NOW() - INTERVAL '1 hour'").Scan(&completed)

	health.Batches["queued_extract"] = queuedExtract
	health.Batches["extracting"] = extracting
	health.Batches["queued_convert"] = queuedConvert
	health.Batches["converting"] = converting
	health.Batches["queued_store"] = queuedStore
	health.Batches["storing"] = storing
	health.Batches["completed_last_hour"] = completed

	return health
}
