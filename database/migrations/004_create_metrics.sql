-- Processing Metrics Table
-- Time-series metrics for monitoring and analytics
CREATE TABLE processing_metrics (
    metric_id BIGSERIAL PRIMARY KEY,
    batch_id VARCHAR(50),
    metric_type VARCHAR(50) NOT NULL,
    metric_value DECIMAL(10, 2) NOT NULL,
    recorded_at TIMESTAMP DEFAULT NOW(),

    FOREIGN KEY (batch_id) REFERENCES batch_processing(batch_id) ON DELETE CASCADE
);

-- Indexes
CREATE INDEX idx_pm_type ON processing_metrics(metric_type, recorded_at);
CREATE INDEX idx_pm_batch ON processing_metrics(batch_id);

-- Comments
COMMENT ON TABLE processing_metrics IS 'Time-series metrics for analytics and monitoring';
COMMENT ON COLUMN processing_metrics.metric_type IS 'Metric types: download_speed, extract_time, convert_time, store_time, queue_depth, worker_utilization, etc.';
COMMENT ON COLUMN processing_metrics.metric_value IS 'Numeric value of the metric (time in seconds, count, percentage, etc.)';
