package metrics

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redlabs-sc/telegram-data-processor-bot-v3/config"
	"go.uber.org/zap"
)

var (
	queueSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "telegram_bot_queue_size",
			Help: "Number of tasks in each queue status",
		},
		[]string{"status"},
	)

	batchProcessingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "telegram_bot_batch_processing_duration_seconds",
			Help:    "Time to process a batch through each stage",
			Buckets: []float64{300, 600, 900, 1200, 1800, 2400, 3600}, // 5min to 1hour
		},
		[]string{"stage"}, // extract, convert, store
	)

	batchStatusCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "telegram_bot_batch_status_count",
			Help: "Number of batches in each status",
		},
		[]string{"status"},
	)

	workerStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "telegram_bot_worker_status",
			Help: "Worker health status (1=healthy, 0=unhealthy)",
		},
		[]string{"type", "id"},
	)

	// Corrected architecture metrics
	extractWorkerActive = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "telegram_bot_extract_worker_active",
			Help: "Extract worker active status (1=processing, 0=idle) - only 1 worker exists",
		},
	)

	convertWorkerActive = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "telegram_bot_convert_worker_active",
			Help: "Convert worker active status (1=processing, 0=idle) - only 1 worker exists",
		},
	)

	storeWorkersActive = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "telegram_bot_store_workers_active",
			Help: "Number of store workers currently processing batches (0-5)",
		},
	)
)

func init() {
	prometheus.MustRegister(queueSize)
	prometheus.MustRegister(batchProcessingDuration)
	prometheus.MustRegister(batchStatusCount)
	prometheus.MustRegister(workerStatus)
	prometheus.MustRegister(extractWorkerActive)
	prometheus.MustRegister(convertWorkerActive)
	prometheus.MustRegister(storeWorkersActive)
}

// StartMetricsServer starts the Prometheus metrics HTTP server
func StartMetricsServer(cfg *config.Config, db *sql.DB, logger *zap.Logger) {
	// Update metrics periodically
	go updateMetrics(db, logger)

	// Create a new HTTP mux for metrics to avoid conflicts
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf(":%d", cfg.MetricsPort)
	logger.Info("Starting metrics server", zap.String("addr", addr))

	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			logger.Error("Metrics server error", zap.Error(err))
		}
	}()
}

func updateMetrics(db *sql.DB, logger *zap.Logger) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Update queue sizes
		var pending, downloading, downloaded, failed int
		db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='PENDING'").Scan(&pending)
		db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='DOWNLOADING'").Scan(&downloading)
		db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='DOWNLOADED'").Scan(&downloaded)
		db.QueryRow("SELECT COUNT(*) FROM download_queue WHERE status='FAILED'").Scan(&failed)

		queueSize.WithLabelValues("pending").Set(float64(pending))
		queueSize.WithLabelValues("downloading").Set(float64(downloading))
		queueSize.WithLabelValues("downloaded").Set(float64(downloaded))
		queueSize.WithLabelValues("failed").Set(float64(failed))

		// Update batch status counts - corrected architecture status values
		var queuedExtract, extracting, queuedConvert, converting, queuedStore, storing, completed int
		db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='QUEUED_EXTRACT'").Scan(&queuedExtract)
		db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='EXTRACTING'").Scan(&extracting)
		db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='QUEUED_CONVERT'").Scan(&queuedConvert)
		db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='CONVERTING'").Scan(&converting)
		db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='QUEUED_STORE'").Scan(&queuedStore)
		db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='STORING'").Scan(&storing)
		db.QueryRow("SELECT COUNT(*) FROM batch_processing WHERE status='COMPLETED'").Scan(&completed)

		batchStatusCount.WithLabelValues("queued_extract").Set(float64(queuedExtract))
		batchStatusCount.WithLabelValues("extracting").Set(float64(extracting))
		batchStatusCount.WithLabelValues("queued_convert").Set(float64(queuedConvert))
		batchStatusCount.WithLabelValues("converting").Set(float64(converting))
		batchStatusCount.WithLabelValues("queued_store").Set(float64(queuedStore))
		batchStatusCount.WithLabelValues("storing").Set(float64(storing))
		batchStatusCount.WithLabelValues("completed").Set(float64(completed))

		// Update worker activity - corrected architecture
		// Extract worker: 1 if EXTRACTING > 0, 0 otherwise
		if extracting > 0 {
			extractWorkerActive.Set(1)
		} else {
			extractWorkerActive.Set(0)
		}

		// Convert worker: 1 if CONVERTING > 0, 0 otherwise
		if converting > 0 {
			convertWorkerActive.Set(1)
		} else {
			convertWorkerActive.Set(0)
		}

		// Store workers: number of batches currently storing (0-5)
		storeWorkersActive.Set(float64(storing))
	}
}
