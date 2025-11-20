# Download Error Fix Summary

## Issue Analysis

Based on the database query results and error logs, the download failures were caused by:

### 1. **404 Errors** (Tasks 7, 9)
- Error: `"http status: 404"`
- Root Cause: The `bot.GetFile()` API call was returning invalid or expired file paths
- Files affected: `Logs_15 November.rar` (2.99GB), `mix 16.11 #332.txt` (96MB)

### 2. **EOF Errors** (Tasks 6, 8)
- Error: `"get file error: Post "http://localhost:8081/bot.../getFile": EOF"`
- Root Cause: The local Telegram Bot API server at `localhost:8081` was closing connections prematurely during the `GetFile` API call for large files
- Files affected: `Logs_14 November.rar` (2.17GB), `DUMP ULP 16.11.2025 Base34 1.txt` (4.18GB)

## Root Cause

The `bot.GetFile()` call in `worker.go:149` was failing for large files because:

1. **No retry logic**: Single API call failure caused immediate task failure
2. **Local Bot API timeout**: The local Bot API server wasn't responding properly for large files
3. **Insufficient error logging**: Couldn't diagnose the exact failure point

## Fixes Implemented

### 1. Added Retry Logic with Exponential Backoff

```go
maxRetries := 3
for attempt := 1; attempt <= maxRetries; attempt++ {
    fileConfig := tgbotapi.FileConfig{FileID: fileID}
    file, err = w.bot.GetFile(fileConfig)

    if err == nil {
        break // Success
    }

    // Log failure and retry with backoff (2s, 4s)
    if attempt < maxRetries {
        backoff := time.Duration(1<<uint(attempt)) * time.Second
        time.Sleep(backoff)
    }
}
```

**Benefits**:
- Handles transient network errors
- Gives local Bot API server time to respond
- Logs each retry attempt for debugging

### 2. Enhanced Error Logging

**Added logging for:**
- GetFile attempts and failures with file_id
- Successful GetFile responses with file_path
- Download URL construction
- HTTP response codes with full URL
- Download progress (bytes written, duration, speed)

**Example log output:**
```json
{
  "level": "WARN",
  "msg": "GetFile attempt failed",
  "attempt": 1,
  "task_id": 6,
  "file_id": "BQACAgEAAxkBAAIF0GkeBAb1QzScAtYNqq8WrqfWPnA7AAJ5HgACaJm4RB_H5tS9Wt6ANgQ",
  "error": "EOF"
}

{
  "level": "INFO",
  "msg": "GetFile succeeded",
  "task_id": 6,
  "file_path": "documents/file_123.rar"
}

{
  "level": "INFO",
  "msg": "Download URL constructed",
  "task_id": 6,
  "url": "http://localhost:8081/file/bot<TOKEN>/documents/file_123.rar"
}

{
  "level": "ERROR",
  "msg": "HTTP download failed",
  "task_id": 7,
  "url": "http://localhost:8081/file/bot<TOKEN>/documents/file_456.rar",
  "status_code": 404,
  "status": "Not Found"
}

{
  "level": "INFO",
  "msg": "Download copy completed",
  "task_id": 6,
  "bytes_written": 2170455167,
  "duration": "5m23s",
  "speed_mbps": 6.42
}
```

### 3. Download Progress Tracking

- Tracks bytes written during `io.Copy()`
- Calculates download duration and speed
- Logs detailed error context if copy fails mid-stream

## Testing Recommendations

### 1. Check Local Bot API Server Status

```bash
# Check if server is running
curl http://localhost:8081/

# Check Docker container status (if using Docker)
docker ps | grep telegram-bot-api

# Check Bot API logs for errors
docker logs telegram-bot-api-container-name --tail 100
```

### 2. Rebuild and Restart

```bash
# Navigate to bot directory
cd /home/redx/-Telegram-data-processor-bot-v3

# Rebuild the coordinator binary
go build -o coordinator cmd/coordinator/main.go

# Restart the bot
./run.sh
```

### 3. Reset Failed Tasks for Retry

```bash
# Reset failed tasks to PENDING for retry
psql -U bot_user -d botv3 << EOF
UPDATE download_queue
SET status = 'PENDING',
    download_attempts = 0,
    last_error = NULL
WHERE task_id IN (6, 7, 8, 9);
EOF
```

### 4. Monitor Logs in Real-Time

```bash
# Watch coordinator logs
tail -f logs/coordinator.log | grep -E "(GetFile|Download|task_id)"

# Check for specific task
tail -f logs/coordinator.log | grep "task_id\":6"
```

## Expected Behavior After Fix

1. **First GetFile attempt fails (EOF)** → Logged with warning
2. **Retry after 2s backoff** → Logged
3. **Second attempt succeeds** → File path logged
4. **Download URL constructed** → Full URL logged
5. **HTTP GET request** → Status code logged
6. **Download completes** → Bytes, duration, speed logged
7. **Task marked DOWNLOADED** → Success

## Files Modified

- `internal/download/worker.go`:
  - Added retry logic for `GetFile` (lines 147-177)
  - Enhanced error logging for HTTP download (lines 224-235)
  - Added download progress tracking (lines 241-260)

## Success Criteria

✅ All 4 failed tasks (6, 7, 8, 9) successfully download
✅ No more EOF errors (or they're handled by retry logic)
✅ 404 errors include full URL for debugging
✅ Large files (4GB) download within timeout period
✅ Logs show clear diagnosis of any remaining issues

---

**Fix Applied**: November 20, 2025
**Branch**: `claude/fix-worker-crashes-01YaHZmHEEMAr1UvDxYMZ12T`
