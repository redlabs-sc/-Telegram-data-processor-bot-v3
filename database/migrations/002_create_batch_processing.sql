-- Batch Processing Table
-- Tracks the lifecycle of each batch through the processing pipeline
-- CORRECTED ARCHITECTURE: Sequential stages with mutex enforcement
CREATE TABLE batch_processing (
    batch_id VARCHAR(50) PRIMARY KEY,
    file_count INT NOT NULL,
    archive_count INT DEFAULT 0,
    txt_count INT DEFAULT 0,

    -- Status follows sequential pipeline: QUEUED_EXTRACT → EXTRACTING → QUEUED_CONVERT → CONVERTING → QUEUED_STORE → STORING → COMPLETED
    status VARCHAR(30) NOT NULL DEFAULT 'QUEUED_EXTRACT' CHECK (status IN (
        'QUEUED_EXTRACT',
        'EXTRACTING',
        'QUEUED_CONVERT',
        'CONVERTING',
        'QUEUED_STORE',
        'STORING',
        'COMPLETED',
        'FAILED_EXTRACT',
        'FAILED_CONVERT',
        'FAILED_STORE'
    )),

    worker_id VARCHAR(50),
    created_at TIMESTAMP DEFAULT NOW(),
    started_at TIMESTAMP,
    completed_at TIMESTAMP,

    -- Stage timing metrics
    extract_started_at TIMESTAMP,
    extract_completed_at TIMESTAMP,
    extract_duration_sec INT,

    convert_started_at TIMESTAMP,
    convert_completed_at TIMESTAMP,
    convert_duration_sec INT,

    store_started_at TIMESTAMP,
    store_completed_at TIMESTAMP,
    store_duration_sec INT,

    total_duration_sec INT,
    error_message TEXT
);

-- Indexes
CREATE INDEX idx_bp_status ON batch_processing(status);
CREATE INDEX idx_bp_created ON batch_processing(created_at);
CREATE INDEX idx_bp_worker ON batch_processing(worker_id);

-- Comments
COMMENT ON TABLE batch_processing IS 'Tracks batch processing lifecycle through sequential stages';
COMMENT ON COLUMN batch_processing.status IS 'Sequential pipeline: QUEUED_EXTRACT → EXTRACTING → QUEUED_CONVERT → CONVERTING → QUEUED_STORE → STORING → COMPLETED/FAILED';
COMMENT ON COLUMN batch_processing.worker_id IS 'ID of worker currently processing this batch (extract, convert, or store worker)';
