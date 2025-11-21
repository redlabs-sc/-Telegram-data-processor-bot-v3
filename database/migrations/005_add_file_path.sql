-- Add file_path column to download_queue
-- This stores the file path from GetFile() response when file is received
-- Prevents issues with local Bot API asynchronous downloads and file_id expiration

ALTER TABLE download_queue
ADD COLUMN file_path TEXT;

COMMENT ON COLUMN download_queue.file_path IS 'File path from GetFile() API call, captured when file is received';
