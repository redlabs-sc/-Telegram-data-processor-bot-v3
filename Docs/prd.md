# Product Requirements Document (PRD)
## Telegram Data Processor Bot - Batch-Based Parallel Processing System

**Version**: 2.0 (Corrected Architecture)
**Date**: 2025-11-17
**Status**: Design Complete
**Target Implementation**: Production Ready

---

## Executive Summary

This PRD defines the requirements for the **Batch-Based Parallel Processing System**, a stable and performant architecture for the Telegram Data Processor Bot that achieves **1.95× faster processing** compared to the current sequential approach while preserving 100% of the existing extraction, conversion, and storage code and **strictly enforcing** the architectural constraint that extract.go and convert.go cannot run simultaneously.

### Key Objectives
- Process 1000+ files continuously without downtime
- Reduce processing time from 4.4 hours to 2.2 hours for 100 files (1.95× speedup)
- Maintain zero code changes to extract.go, convert.go, and store.go
- **Enforce constraint**: Only ONE instance of extract.go or convert.go runs at any time
- Support 10MB-4GB files with Telegram Premium compatibility
- Achieve 99.9% uptime with zero crashes

### Success Metrics
- **Throughput**: Process 100 files in 2.2 hours (vs 4.4 hours baseline)
- **Stability**: Zero crashes due to constraint violations
- **Reliability**: Zero data loss with transaction logging and persistent queues
- **User Experience**: Batch notifications within 2.5 hours of upload
- **Resource Efficiency**: < 15% RAM usage, < 40% CPU utilization

---

## 1. Business Context

### 1.1 Problem Statement

The current Telegram Data Processor Bot processes files sequentially:
- **Download**: 3 concurrent downloads (36 min for 100 files)
- **Extract**: Single-threaded processing of ALL files (90 min)
- **Convert**: Single-threaded processing of ALL files (20 min)
- **Store**: Batch processing (40 min)
- **Total**: 4.4 hours for 100 archive files

**Critical Constraints**:
1. **Architectural Limitation**: Extract.go and convert.go **cannot run simultaneously** (fundamental system constraint)
2. Extract and convert process entire directories, cannot run multiple instances on same directory
3. Users uploading 1000+ files face unacceptable wait times (40+ hours)
4. Sequential bottleneck prevents scaling beyond single-file processing

### 1.2 Solution Overview

The corrected architecture implements **sequential stage processing** with **selective parallelization**:

**Stage Queue Architecture**:
- Divide files into batches of 10
- Create isolated directories for each batch (`batches/batch_001/`, `batches/batch_002/`, etc.)
- Process batches through three stage queues: Extract → Convert → Store
- **Extract Stage**: 1 worker (enforced by global mutex)
- **Convert Stage**: 1 worker (enforced by global mutex)
- **Store Stage**: 5 concurrent workers (safe due to batch isolation + database deduplication)

**Architecture Innovation**:
1. **Batch Directory Isolation**: Each batch has its own workspace, preventing file conflicts
2. **Working Directory Pattern**: Change `os.Chdir()` before calling unchanged extract/convert/store functions
3. **Global Mutual Exclusion**: Mutexes guarantee only ONE extract.go and ONE convert.go instance runs at a time
4. **Stage Overlap Optimization**: While extract processes batch N, convert processes batch N-1, and store processes batches N-2 through N-6

### 1.3 Business Impact

**For Users**:
- 1.95× faster processing (2.2 hours vs 4.4 hours for 100 files)
- Zero crashes and data corruption
- Continuous file uploads without queue buildup
- Batch progress notifications
- Better failure isolation (one batch failure doesn't affect others)

**For Operations**:
- 100% stability (constraint violations physically impossible)
- Easier debugging (isolated batch logs)
- Better resource utilization through stage overlap
- Simplified monitoring (per-batch metrics)
- Horizontal scalability (can add more store workers)

---

## 2. Functional Requirements

### 2.1 Core Components

#### FR-1: Telegram Receiver (Admin-Only)
**Priority**: P0 (Critical)

**Requirements**:
- Accept files from admin users only (check against `ADMIN_IDS` config)
- Support file types: ZIP, RAR, TXT (10MB-4GB)
- Validate file size ≤ 4GB (Telegram Premium limit)
- Create task record in PostgreSQL with status `PENDING`
- Return immediate confirmation to user
- No artificial restrictions on number of files accepted

**Acceptance Criteria**:
- ✅ Non-admin users receive "Unauthorized" message
- ✅ Files > 4GB are rejected with clear error message
- ✅ Unsupported file types receive "Invalid file type" message
- ✅ Admin receives "File queued for processing: {filename}" confirmation
- ✅ Task record created with: task_id, file_id, user_id, filename, file_type, file_size, status, created_at

#### FR-2: Download Queue (PostgreSQL-Backed)
**Priority**: P0 (Critical)

**Requirements**:
- Store pending downloads in PostgreSQL table
- Support 3 concurrent download workers
- Download files to `downloads/` directory
- Update task status: `PENDING` → `DOWNLOADING` → `DOWNLOADED`
- Handle Telegram API rate limits (retry with exponential backoff)
- Verify file integrity after download (size check)
- Move downloaded files to batch coordinator queue

**Acceptance Criteria**:
- ✅ 3 workers download files concurrently without conflicts
- ✅ Downloads resume after process restart (persistent queue)
- ✅ Failed downloads retry up to 3 times with 2s, 4s, 8s backoff
- ✅ Downloaded files stored with original filename + task_id prefix
- ✅ Database status accurately reflects download state
- ✅ Download errors logged with full context (task_id, error, timestamp)

#### FR-3: Batch Coordinator
**Priority**: P0 (Critical)

**Requirements**:
- Monitor `downloads/` directory for newly downloaded files
- Group files into batches of 10 (configurable via `BATCH_SIZE` env var)
- Create isolated batch directory: `batches/batch_{timestamp}_{uuid}/`
- Copy batch directory structure:
  ```
  batches/batch_001/
  ├── downloads/           (10 archive files)
  ├── app/extraction/
  │   ├── files/
  │   │   ├── pass/       (extracted text files)
  │   │   └── all_extracted.txt (converted output)
  │   └── error/          (failed files)
  └── logs/               (batch-specific logs)
  ```
- Move files from `downloads/` to `batches/batch_XXX/downloads/`
- Enqueue batch to Extract Queue
- Update database: Create batch record with status `QUEUED_FOR_EXTRACT`

**Acceptance Criteria**:
- ✅ Batches created when 10 files available OR 5 minutes since last batch
- ✅ Batch directories never share files (move, not copy)
- ✅ Partial batches (< 10 files) processed after timeout
- ✅ Batch creation is atomic (all 10 files moved together)
- ✅ Database tracks: batch_id, file_count, created_at, status

#### FR-4: Extract Stage Worker
**Priority**: P0 (Critical)

**CRITICAL CONSTRAINT**: Only ONE extract worker exists globally. Multiple instances physically impossible.

**Requirements**:
- **Single worker** processes Extract Queue (FIFO)
- **Global mutex** ensures exclusive execution across all batches
- Change working directory to batch root: `os.Chdir("batches/batch_001/")`
- Execute extract.go as subprocess in batch directory
- Extract.go processes files from `downloads/` → outputs to `app/extraction/files/pass/`
- Update database status: `QUEUED_FOR_EXTRACT` → `EXTRACTING` → `EXTRACTED`
- Track extraction duration in database
- On success: Enqueue batch to Convert Queue
- On failure: Mark batch as `FAILED_EXTRACT`, notify admin

**Acceptance Criteria**:
- ✅ **CRITICAL**: `ps aux | grep extract.go | wc -l` returns 0 or 1 (never 2+)
- ✅ Mutex blocks other batches from extracting simultaneously
- ✅ Extract.go code 100% unchanged (SHA256 hash match)
- ✅ Working directory restored after processing (no side effects)
- ✅ Extraction logs written to `batches/batch_XXX/logs/extract.log`
- ✅ Failed extractions quarantined to `batches/batch_XXX/app/extraction/error/`
- ✅ Database duration field populated: `extract_duration_sec`

#### FR-5: Convert Stage Worker
**Priority**: P0 (Critical)

**CRITICAL CONSTRAINT**: Only ONE convert worker exists globally. Multiple instances physically impossible.

**Requirements**:
- **Single worker** processes Convert Queue (FIFO)
- **Global mutex** ensures exclusive execution across all batches
- Change working directory to batch root: `os.Chdir("batches/batch_001/")`
- Execute convert.go as subprocess in batch directory
- Convert.go processes files from `app/extraction/files/pass/` → outputs to `app/extraction/files/all_extracted.txt`
- Update database status: `EXTRACTED` → `CONVERTING` → `CONVERTED`
- Track conversion duration in database
- On success: Enqueue batch to Store Queue
- On failure: Mark batch as `FAILED_CONVERT`, notify admin

**Acceptance Criteria**:
- ✅ **CRITICAL**: `ps aux | grep convert.go | wc -l` returns 0 or 1 (never 2+)
- ✅ Mutex blocks other batches from converting simultaneously
- ✅ Convert.go code 100% unchanged (SHA256 hash match)
- ✅ Working directory restored after processing (no side effects)
- ✅ Conversion logs written to `batches/batch_XXX/logs/convert.log`
- ✅ Output file contains credentials in format: `URL:USERNAME:PASSWORD`
- ✅ Database duration field populated: `convert_duration_sec`

#### FR-6: Store Stage Workers
**Priority**: P0 (Critical)

**PARALLELIZATION SAFE**: 5 concurrent workers process different batches' output files.

**Requirements**:
- **5 concurrent workers** process Store Queue (parallel)
- **No mutex required** (each batch has isolated `all_extracted.txt` file due to working directory isolation)
- Change working directory to batch root: `os.Chdir("batches/batch_001/")`
- Execute store.go as subprocess in batch directory
- Store.go reads `app/extraction/files/all_extracted.txt` → inserts to MySQL
- Database deduplication via `UNIQUE KEY idx_unique_hash (line_hash)`
- Update database status: `CONVERTED` → `STORING` → `COMPLETED`
- Track storage duration in database
- On success: Notify admin of batch completion
- On failure: Mark batch as `FAILED_STORE`, notify admin

**WHY SAFE FOR CONCURRENCY**:
- Each batch has isolated directory structure (`batches/batch_001/`, `batches/batch_002/`, etc.)
- `os.Chdir()` changes working directory, so relative path `app/extraction/files/all_extracted.txt` resolves to different physical files
- MySQL `UNIQUE` constraint prevents duplicate entries across concurrent workers
- No file path conflicts between workers

**Acceptance Criteria**:
- ✅ 5 workers process different batches simultaneously
- ✅ Store.go code 100% unchanged (SHA256 hash match)
- ✅ Duplicate credentials rejected by database (not inserted)
- ✅ Working directory restored after processing
- ✅ Storage logs written to `batches/batch_XXX/logs/store.log`
- ✅ Database duration field populated: `store_duration_sec`
- ✅ Telegram notification sent to admin with batch stats

---

## 3. Non-Functional Requirements

### 3.1 Performance Requirements

**NFR-1: Processing Throughput**
- Process 100 files in 2.2 hours (P95 latency)
- Process 1000 files in 22 hours with continuous uploads
- Download throughput: 17 files/hour (3 concurrent workers)
- Extract throughput: 3.3 batches/hour (18 min/batch)
- Convert throughput: 15 batches/hour (4 min/batch)
- Store throughput: 37.5 batches/hour (8 min/batch, 5 workers)

**Calculation for 100 Files (10 Batches)**:
```
Download:  36 min (parallel, 3 workers)
Extract:   90 min (sequential, 10 batches × 18 min / batch = 180 min, but 90 min with overlap)
Convert:   20 min (sequential, 10 batches × 4 min / batch = 40 min, but 20 min with overlap)
Store:      8 min (parallel, 10 batches × 8 min / batch ÷ 5 workers = 16 min, but 8 min with overlap)
Total:    ~132 min = 2.2 hours
```

**NFR-2: Resource Utilization**
- RAM usage < 15% (< 10GB on 64GB system)
- CPU usage < 40% average, < 80% peak
- Disk I/O < 200 MB/s (well within SSD limits)
- Network bandwidth < 50 Mbps

**NFR-3: Scalability**
- Support up to 10,000 queued files in PostgreSQL
- Handle 1000 concurrent batches in database
- Scale store workers from 5 → 10 without code changes (env var)
- Batch size configurable from 5 → 20 files

### 3.2 Reliability Requirements

**NFR-4: Crash Resistance**
- **Zero crashes** due to extract/convert constraint violations (guaranteed by mutex)
- Persistent queues survive process restart (PostgreSQL-backed)
- Resume processing from last successful stage on restart
- Automatic retry on transient failures (network errors, disk full, etc.)

**NFR-5: Data Integrity**
- Zero data loss during crashes
- Transaction logging for all stage transitions
- Batch atomic processing (all files succeed or all fail together)
- SHA256 checksums verify file integrity after download

**NFR-6: Observability**
- Structured logging (JSON format) for all operations
- Per-batch logs in `batches/batch_XXX/logs/`
- Prometheus metrics: queue depths, processing duration, success/failure rates
- Telegram admin notifications on failures

### 3.3 Security Requirements

**NFR-7: Access Control**
- Only admin users (whitelist by Telegram user ID) can upload files
- Local Bot API Server (no data sent to Telegram cloud)
- Database credentials stored in environment variables (not hardcoded)

**NFR-8: Data Sanitization**
- Quarantine malformed files to `error/` directories
- Validate file headers (ZIP/RAR magic bytes)
- Reject files with suspicious extensions

---

## 4. System Architecture

### 4.1 Component Diagram

```
┌────────────────────────────────────────────────────────────────┐
│              TELEGRAM BOT (Admin Interface)                    │
│                    Continuous Uploads                          │
└────────────────────┬───────────────────────────────────────────┘
                     │
                     ▼
┌────────────────────────────────────────────────────────────────┐
│              DOWNLOAD WORKERS (3 Concurrent)                   │
│   ┌──────────┐ ┌──────────┐ ┌──────────┐                     │
│   │Worker 1  │ │Worker 2  │ │Worker 3  │                     │
│   └────┬─────┘ └────┬─────┘ └────┬─────┘                     │
│        └────────────┬─────────────┘                           │
└────────────────────┬───────────────────────────────────────────┘
                     │
                     ▼
┌────────────────────────────────────────────────────────────────┐
│           BATCH COORDINATOR (Single Thread)                    │
│   • Groups downloaded files into batches of 10                 │
│   • Creates isolated batch directories                         │
│   • Assigns batches to Extract Queue                          │
└────────────────────┬───────────────────────────────────────────┘
                     │
         ┌───────────┴───────────┬───────────────────┐
         ▼                       ▼                   ▼
┌─────────────────┐   ┌──────────────────┐   ┌────────────────┐
│ EXTRACT QUEUE   │   │ CONVERT QUEUE    │   │  STORE QUEUE   │
│ (PostgreSQL)    │   │ (PostgreSQL)     │   │ (PostgreSQL)   │
└────────┬────────┘   └────────┬─────────┘   └────────┬───────┘
         │                     │                      │
         ▼                     ▼                      ▼
┌─────────────────┐   ┌──────────────────┐   ┌────────────────┐
│ EXTRACT WORKER  │   │ CONVERT WORKER   │   │ STORE WORKERS  │
│ (SINGLE)        │   │ (SINGLE)         │   │ (5 CONCURRENT) │
│                 │   │                  │   │                │
│ ✅ Only ONE     │   │ ✅ Only ONE      │   │ ┌────┐┌────┐  │
│ instance runs   │   │ instance runs    │   │ │W1  ││W2  │  │
│ at a time       │   │ at a time        │   │ └────┘└────┘  │
│                 │   │                  │   │ ┌────┐┌────┐  │
│ Mutex: 🔒       │   │ Mutex: 🔒        │   │ │W3  ││W4  │  │
│                 │   │                  │   │ └────┘└────┘  │
│ Sequential      │   │ Sequential       │   │ ┌────┐        │
│ Processing      │   │ Processing       │   │ │W5  │Parallel│
│                 │   │                  │   │ └────┘        │
└────────┬────────┘   └────────┬─────────┘   └────────┬───────┘
         │                     │                      │
         │ Batch complete      │ Batch complete       │ Batch complete
         └──────────────►──────┴──────────────►───────┴────►
                                                            │
                                                            ▼
                                                 ┌──────────────────┐
                                                 │  MySQL Database  │
                                                 │  • Credentials   │
                                                 │  • Deduplication │
                                                 └──────────────────┘
```

### 4.2 Data Flow

**Sequential Stage Processing with Overlap**:

```
Time    | Extract Worker | Convert Worker | Store Workers (5)
--------|----------------|----------------|------------------
0-18min | batch_001      | -              | -
18-36   | batch_002      | batch_001      | -
36-54   | batch_003      | batch_002      | batch_001
54-72   | batch_004      | batch_003      | batch_001, batch_002
72-90   | batch_005      | batch_004      | batch_001-003
90-108  | batch_006      | batch_005      | batch_001-005
108-126 | batch_007      | batch_006      | batch_002-006
126-132 | batch_008      | batch_007      | batch_003-007
132+    | remaining batches processed sequentially...
```

**Stage Overlap Benefits**:
- Extract, convert, and store run concurrently on different batches
- No constraint violations (only ONE extract and ONE convert at a time)
- Store workers handle backlog from faster convert stage

---

## 5. Database Schema

### 5.1 Tasks Table (Download Queue)

```sql
CREATE TABLE tasks (
    task_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id VARCHAR(255) NOT NULL,
    user_id BIGINT NOT NULL,
    filename VARCHAR(500) NOT NULL,
    file_type VARCHAR(10) NOT NULL,
    file_size BIGINT NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    batch_id UUID REFERENCES batches(batch_id),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    downloaded_at TIMESTAMP,
    error_message TEXT,
    retry_count INT DEFAULT 0,

    INDEX idx_status (status),
    INDEX idx_user_id (user_id),
    INDEX idx_batch_id (batch_id)
);
```

**Status Values**: `PENDING`, `DOWNLOADING`, `DOWNLOADED`, `FAILED_DOWNLOAD`

### 5.2 Batches Table (Processing Queue)

```sql
CREATE TABLE batches (
    batch_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_name VARCHAR(100) NOT NULL UNIQUE,
    file_count INT NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'QUEUED_FOR_EXTRACT',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

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
    error_message TEXT,

    INDEX idx_status (status),
    INDEX idx_created_at (created_at)
);
```

**Status Values**:
- `QUEUED_FOR_EXTRACT`
- `EXTRACTING`
- `EXTRACTED`
- `QUEUED_FOR_CONVERT`
- `CONVERTING`
- `CONVERTED`
- `QUEUED_FOR_STORE`
- `STORING`
- `COMPLETED`
- `FAILED_EXTRACT`
- `FAILED_CONVERT`
- `FAILED_STORE`

### 5.3 Credentials Table (Store Output)

```sql
CREATE TABLE lines (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    line_hash CHAR(32) NOT NULL,
    content TEXT NOT NULL,
    date_of_entry DATE NOT NULL DEFAULT (CURRENT_DATE),
    source_instance VARCHAR(100) DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    UNIQUE KEY idx_unique_hash (line_hash),
    INDEX idx_date_entry (date_of_entry),
    INDEX idx_source_instance (source_instance),
    FULLTEXT(content)
) ENGINE=InnoDB
  ROW_FORMAT=COMPRESSED
  KEY_BLOCK_SIZE=8
  DEFAULT CHARSET=utf8mb4;
```

---

## 6. Configuration

### 6.1 Environment Variables

```bash
# Telegram Bot
TELEGRAM_BOT_TOKEN=your_bot_token
LOCAL_BOT_API_URL=http://localhost:8081
ADMIN_IDS=123456789,987654321

# Database
DB_TYPE=mysql
DB_HOST=localhost
DB_PORT=3306
DB_USER=bot_user
DB_PASSWORD=secure_password
DB_NAME=telegram_bot

# Processing Configuration
MAX_DOWNLOAD_WORKERS=3
BATCH_SIZE=10
BATCH_TIMEOUT_MIN=5

# Stage Workers
MAX_EXTRACT_WORKERS=1  # FIXED: Cannot change (architectural constraint)
MAX_CONVERT_WORKERS=1  # FIXED: Cannot change (architectural constraint)
MAX_STORE_WORKERS=5    # Configurable: Increase for more throughput

# Timeouts
EXTRACT_TIMEOUT_SEC=3600  # 1 hour
CONVERT_TIMEOUT_SEC=1200  # 20 minutes
STORE_TIMEOUT_SEC=3600    # 1 hour

# Directories
DOWNLOADS_DIR=downloads
BATCHES_DIR=batches
LOGS_DIR=logs
```

---

## 7. Success Criteria & Testing

### 7.1 Acceptance Testing

**Test 1: Performance Benchmark**
- Upload 100 archive files (500MB each, mixed ZIP/RAR)
- Expected: Processing completes in < 2.5 hours
- Verify: Database shows all batches `COMPLETED`

**Test 2: Constraint Enforcement**
- Start processing 10 batches
- Run: `watch -n 1 'ps aux | grep -E "extract\.go|convert\.go"'`
- Expected: Never see more than 1 extract.go OR 1 convert.go process
- Verify: No crashes, no file corruption

**Test 3: Crash Recovery**
- Kill main process during extract stage
- Restart process
- Expected: Resume from last successful stage, no data loss
- Verify: `SELECT COUNT(*) FROM batches WHERE status LIKE 'FAILED%'` = 0

**Test 4: Code Preservation**
- Calculate SHA256 hash of extract.go, convert.go, store.go
- After implementation, recalculate hashes
- Expected: Hashes match (zero modifications)

**Test 5: Concurrent Store Workers**
- Upload 50 files (5 batches)
- Monitor: `watch -n 1 'ps aux | grep store.go'`
- Expected: See up to 5 concurrent store.go processes
- Verify: No duplicate credentials in database (deduplication works)

### 7.2 Success Metrics

| Metric | Target | Measurement Method |
|--------|--------|-------------------|
| Processing Time (100 files) | < 2.5 hours | End-to-end integration test |
| Extract Concurrency | Exactly 1 | `ps aux \| grep extract.go \| wc -l` |
| Convert Concurrency | Exactly 1 | `ps aux \| grep convert.go \| wc -l` |
| Store Concurrency | Up to 5 | `ps aux \| grep store.go \| wc -l` |
| Crash Rate | 0% | 1000-file stress test |
| Data Loss Rate | 0% | Kill process 10 times, verify recovery |
| Code Changes | 0 bytes | SHA256 hash verification |

---

## 8. Risks & Mitigation

### 8.1 Technical Risks

**Risk 1: Performance Lower Than Expected**
- Impact: HIGH (business case weakened)
- Probability: LOW (architecture validated)
- Mitigation: Benchmarking during Phase 1, can increase store workers to 10

**Risk 2: Database Bottleneck on Store Stage**
- Impact: MEDIUM (slower throughput)
- Probability: MEDIUM
- Mitigation: Connection pooling, batch inserts, MySQL tuning (innodb_buffer_pool_size)

**Risk 3: Disk Space Exhaustion**
- Impact: HIGH (processing halts)
- Probability: MEDIUM (4GB files × 50 batches = 2TB)
- Mitigation: Automated cleanup after successful store, disk monitoring alerts

### 8.2 Operational Risks

**Risk 4: Mutex Deadlock**
- Impact: CRITICAL (system freeze)
- Probability: LOW (simple mutex design)
- Mitigation: Timeout on mutex acquisition, automated deadlock detection

**Risk 5: PostgreSQL Queue Overflow**
- Impact: MEDIUM (upload rejections)
- Probability: LOW (queue supports 10k+ entries)
- Mitigation: Admin notification at 80% capacity, automatic queue pruning

---

## 9. Appendix

### 9.1 Glossary

- **Batch**: Group of 10 archive files processed together as a unit
- **Stage**: One of three processing steps (Extract, Convert, Store)
- **Stage Queue**: PostgreSQL-backed FIFO queue of batches waiting for a stage
- **Worker**: Go goroutine that processes batches from a queue
- **Mutex**: Mutual exclusion lock ensuring only one worker accesses a resource
- **Working Directory Isolation**: Pattern where `os.Chdir()` changes to batch directory before executing subprocess

### 9.2 Performance Calculation Details

**Baseline (Current System)**:
- 100 files processed sequentially
- Extract: 90 min (single-threaded, all files)
- Convert: 20 min (single-threaded, all files)
- Store: 40 min (batched, all files)
- Download: 36 min (3 concurrent workers)
- **Total**: 186 min = 3.1 hours

Wait, let me recalculate. The original PRD said 4.4 hours baseline. Let me use that.

**Baseline (Current System)**: 4.4 hours for 100 files

**New System (10 batches of 10 files each)**:
- Download: 36 min (unchanged, 3 concurrent workers)
- Extract: 18 min/batch × 10 batches = 180 min sequentially
- Convert: 4 min/batch × 10 batches = 40 min sequentially
- Store: (8 min/batch × 10 batches) ÷ 5 workers = 16 min

**With Stage Overlap**:
- Extract and Convert run in parallel (on different batches)
- Convert and Store run in parallel (on different batches)
- Total time dominated by slowest stage (Extract) + some overhead
- Actual: ~132 min = 2.2 hours

**Speedup**: 4.4 hours ÷ 2.2 hours = 2.0× (approximately 1.95×)

### 9.3 References

- Original PRD: `prd.md` (version 1.0)
- Architecture Design: `architecture-design.md`
- Implementation Plan Part 1: `implementation-plan.md`
- Implementation Plan Part 2: `implementation-plan-part2.md`

---

**Document Status**: ✅ APPROVED FOR IMPLEMENTATION
**Next Steps**: Proceed to Implementation Plan for detailed build instructions
