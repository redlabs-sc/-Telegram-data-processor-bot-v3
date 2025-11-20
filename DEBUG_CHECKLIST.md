# Telegram Bot Download Error Debug Checklist

## Issue Summary
Files are being queued successfully but failing during download with:
- **404 errors**: File not found
- **EOF errors**: Connection closed by local Bot API server (large files)

## Debug Steps

### 1. Check Local Bot API Server
```bash
# Check if local Bot API is running
curl http://localhost:8081/
docker ps | grep telegram-bot-api

# Check Bot API logs for errors
docker logs telegram-bot-api-container-name

# Restart the local Bot API server if needed
docker restart telegram-bot-api-container-name
```

### 2. Verify File Information in Database
```sql
-- Connect to database
psql -U bot_user -d botv3

-- Check failed tasks
SELECT task_id, file_id, filename, file_type, file_size, status, last_error
FROM download_queue
WHERE status = 'FAILED'
ORDER BY task_id DESC
LIMIT 10;

-- Check if file_id is stored
SELECT task_id, file_id, LENGTH(file_id) as file_id_length
FROM download_queue
WHERE task_id IN (6, 7, 8, 9);
```

### 3. Common Issues and Fixes

#### Issue A: file_id vs file_path confusion
The bot might be storing `file_id` but needs `file_path` for the local Bot API, or vice versa.

**Fix**: When queuing files, ensure you're storing the correct identifier:
```go
// For local Bot API server, you need file_path, not file_id
filePath := update.Message.Document.FilePath
// OR get it via getFile API call
file, err := bot.GetFile(tgbotapi.FileConfig{FileID: update.Message.Document.FileID})
if err != nil {
    log.Error("Failed to get file:", err)
    return
}
filePath = file.FilePath
```

#### Issue B: Local Bot API server timeout on large files
The local API server might be timing out when downloading large files from Telegram.

**Fix**: Increase timeouts in your local Bot API server configuration:
```bash
# When running telegram-bot-api container
docker run -d \
  -v /path/to/storage:/var/lib/telegram-bot-api \
  -p 8081:8081 \
  --name telegram-bot-api \
  -e TELEGRAM_API_ID=your_api_id \
  -e TELEGRAM_API_HASH=your_api_hash \
  -e TELEGRAM_MAX_DOWNLOAD_FILE_SIZE=4294967296 \
  telegram-bot-api/telegram-bot-api
```

#### Issue C: File expired or deleted
Telegram files might expire if not downloaded quickly enough.

**Fix**: Download files immediately when received, before queuing for processing.

### 4. Code Fixes Needed

#### Fix in receiver.go (Telegram file reception)
Ensure you're storing the FULL file information:

```go
// WRONG - might cause 404
task := Task{
    FileID: update.Message.Document.FileID,  // This might not work with local API
}

// CORRECT - store file_path for local Bot API
fileConfig := tgbotapi.FileConfig{FileID: update.Message.Document.FileID}
file, err := bot.GetFile(fileConfig)
if err != nil {
    log.Error("Failed to get file info:", err)
    return
}

task := Task{
    FileID: update.Message.Document.FileID,
    FilePath: file.FilePath,  // Store this for download worker
}
```

#### Fix in worker.go (File download)
Use the correct download URL:

```go
// For local Bot API server
downloadURL := fmt.Sprintf("http://localhost:8081/file/bot%s/%s",
    botToken, task.FilePath)

// NOT this (standard API, won't work for large files):
// downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s",
//     botToken, task.FileID)
```

### 5. Verify Local Bot API Configuration

Check your `.env` file:
```env
# Make sure these are set correctly
USE_LOCAL_BOT_API=true
LOCAL_BOT_API_URL=http://localhost:8081
TELEGRAM_BOT_TOKEN=8383132189:AAGzX3APQYJmO1LMC6EOZHzf0LyZUWyL0h8

# Make sure bot token matches the one used by local API server
```

### 6. Test Download Manually

Test if you can download a file manually:
```bash
# Get file info
curl -X POST "http://localhost:8081/bot<YOUR_TOKEN>/getFile" \
  -d '{"file_id":"<FILE_ID>"}' \
  -H "Content-Type: application/json"

# Download file (use file_path from above response)
curl "http://localhost:8081/file/bot<YOUR_TOKEN>/<FILE_PATH>" \
  -o test_download.file
```

### 7. Check for Network Issues
```bash
# Check if localhost:8081 is accessible
telnet localhost 8081
# OR
nc -zv localhost 8081

# Check if Bot API server is healthy
curl -v http://localhost:8081/healthz
```

## Next Steps

1. Run the database query in step 2 to check what's stored in `file_id` column
2. Check if local Bot API server is running and accessible
3. Share the bot source code (receiver.go and worker.go) so we can identify the exact issue
4. Check Bot API server logs for errors during file downloads

## Expected Resolution

Once we identify whether the issue is:
- Missing `file_path` in database → Update receiver.go to store file_path
- Local Bot API timeout → Increase timeout configurations
- File expiration → Implement immediate download strategy
- Wrong URL construction → Fix download URL in worker.go

We can provide a targeted fix.
