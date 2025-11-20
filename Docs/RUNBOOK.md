# Operations Runbook: Telegram Data Processor Bot

## Architecture Overview

**Corrected Architecture (1.95× speedup)**:
- 3 download workers (concurrent)
- 1 extract worker (mutex enforced)
- 1 convert worker (mutex enforced)
- 5 store workers (concurrent - batch isolation)

**Processing Time**: ~2.2 hours for 100 files (vs 4.4 hours sequential)

---

## Quick Start

### Development Environment

```bash
# 1. Clone repository
git clone https://github.com/redlabs-sc/telegram-data-processor-bot-v3.git
cd telegram-data-processor-bot-v3

# 2. Create environment file
cp .env.example .env
# Edit .env with your values

# 3. Start all services
docker-compose up -d

# 4. Check health
curl http://localhost:8080/health

# 5. View logs
docker-compose logs -f coordinator
```

### Production (Kubernetes)

```bash
# 1. Create secrets
kubectl create secret generic telegram-bot-secrets \
  --from-literal=TELEGRAM_BOT_TOKEN="your_token" \
  --from-literal=ADMIN_IDS="123456789" \
  --from-literal=DB_PASSWORD="secure_password" \
  --from-literal=DB_USER="bot_user"

# 2. Deploy
kubectl apply -f k8s/

# 3. Check status
kubectl get pods -l app=telegram-bot
kubectl logs -l app=telegram-bot -f
```

---

## Monitoring

### Health Endpoints

```bash
# Main health check
curl http://localhost:8080/health

# Liveness probe (for Kubernetes)
curl http://localhost:8080/health/live

# Readiness probe (for Kubernetes)
curl http://localhost:8080/health/ready
```

### Prometheus Metrics

```bash
# Metrics endpoint
curl http://localhost:9090/metrics

# Key metrics to watch:
# - telegram_bot_queue_size
# - telegram_bot_extract_worker_active
# - telegram_bot_convert_worker_active
# - telegram_bot_store_workers_active
# - telegram_bot_batches_completed_total
```

### Grafana Dashboard

1. Open http://localhost:3000
2. Login: admin / admin
3. Navigate to "Telegram Bot - Option 2" dashboard

---

## Common Operations

### Check Queue Status

```bash
# Via Telegram bot
/queue

# Via database
psql -U bot_user -d telegram_bot_option2 -c "
  SELECT status, COUNT(*)
  FROM download_queue
  GROUP BY status;
"
```

### Check Batch Status

```bash
# Via Telegram bot
/batches

# Via database
psql -U bot_user -d telegram_bot_option2 -c "
  SELECT batch_id, status, file_count,
         extract_duration_sec, convert_duration_sec, store_duration_sec
  FROM batch_processing
  ORDER BY created_at DESC
  LIMIT 10;
"
```

### View System Statistics

```bash
# Via Telegram bot
/stats

# Via database - last 24 hours
psql -U bot_user -d telegram_bot_option2 -c "
  SELECT
    COUNT(*) as total_batches,
    SUM(file_count) as total_files,
    AVG(extract_duration_sec + convert_duration_sec + store_duration_sec)/60 as avg_minutes
  FROM batch_processing
  WHERE status = 'COMPLETED'
    AND completed_at > NOW() - INTERVAL '24 hours';
"
```

---

## Troubleshooting

### Problem: Files Not Downloading

**Symptoms**: Files stuck in PENDING status

**Diagnosis**:
```bash
# Check download workers
curl http://localhost:8080/health | jq '.components'

# Check for errors
docker-compose logs coordinator | grep -i "download"

# Check database
psql -U bot_user -d telegram_bot_option2 -c "
  SELECT * FROM download_queue
  WHERE status = 'PENDING'
  ORDER BY created_at DESC
  LIMIT 5;
"
```

**Solutions**:
1. Verify Telegram Bot Token is valid
2. Check Local Bot API Server is running
3. Verify network connectivity
4. Check disk space for downloads

### Problem: Batches Stuck in EXTRACTING

**Symptoms**: Batch stays in EXTRACTING for > 30 minutes

**Diagnosis**:
```bash
# Check stuck batches
psql -U bot_user -d telegram_bot_option2 -c "
  SELECT batch_id, status, started_at
  FROM batch_processing
  WHERE status = 'EXTRACTING'
    AND started_at < NOW() - INTERVAL '30 minutes';
"

# Check batch logs
cat batches/batch_XXX/logs/extract.log
```

**Solutions**:
1. Check extract.go for errors
2. Verify pass.txt file exists
3. Check for corrupted archives
4. Manually reset batch (see Manual Interventions)

### Problem: Mutex Violation Alert

**Symptoms**: /health shows "MUTEX VIOLATION!" for extract or convert

**Diagnosis**:
```bash
# Check concurrent operations
psql -U bot_user -d telegram_bot_option2 -c "
  SELECT status, COUNT(*)
  FROM batch_processing
  WHERE status IN ('EXTRACTING', 'CONVERTING')
  GROUP BY status;
"
```

**Solution**:
This is a critical architectural violation. Restart the coordinator:
```bash
docker-compose restart coordinator
# or
kubectl rollout restart deployment/telegram-bot-coordinator
```

### Problem: High Memory Usage

**Symptoms**: Memory usage > 80%

**Diagnosis**:
```bash
# Check container stats
docker stats telegram-bot-coordinator

# Check Go memory
curl http://localhost:9090/metrics | grep process_resident_memory
```

**Solutions**:
1. Reduce MAX_STORE_WORKERS (try 3 instead of 5)
2. Check for memory leaks in logs
3. Restart coordinator to release memory

### Problem: Database Connection Failed

**Symptoms**: Health check shows database unhealthy

**Diagnosis**:
```bash
# Check PostgreSQL
docker-compose exec postgres pg_isready -U bot_user

# Check connection string
docker-compose exec coordinator env | grep DB_
```

**Solutions**:
1. Verify PostgreSQL is running
2. Check credentials in .env
3. Check network connectivity
4. Restart PostgreSQL container

---

## Manual Interventions

### Reset Stuck Download

```bash
psql -U bot_user -d telegram_bot_option2 -c "
  UPDATE download_queue
  SET status = 'PENDING',
      download_attempts = 0,
      started_at = NULL
  WHERE task_id = XXX;
"
```

### Reset Stuck Batch

```bash
psql -U bot_user -d telegram_bot_option2 -c "
  UPDATE batch_processing
  SET status = 'QUEUED_EXTRACT',
      started_at = NULL,
      extract_duration_sec = NULL,
      convert_duration_sec = NULL,
      store_duration_sec = NULL,
      last_error = NULL
  WHERE batch_id = 'batch_XXX';
"
```

### Mark Failed Batch

```bash
psql -U bot_user -d telegram_bot_option2 -c "
  UPDATE batch_processing
  SET status = 'FAILED_EXTRACT',
      last_error = 'Manually failed',
      completed_at = NOW()
  WHERE batch_id = 'batch_XXX';
"
```

### Clear Pending Queue

```bash
# DANGER: Only use in emergencies
psql -U bot_user -d telegram_bot_option2 -c "
  DELETE FROM download_queue WHERE status = 'PENDING';
"
```

### Force Cleanup Completed Batches

```bash
# Remove batch directories older than 1 hour
find batches -maxdepth 1 -type d -mmin +60 -name "batch_*" -exec rm -rf {} \;
```

---

## Backup & Recovery

### Database Backup

```bash
# Manual backup
pg_dump -U bot_user telegram_bot_option2 | gzip > backup_$(date +%Y%m%d).sql.gz

# Automated daily backup (cron)
0 2 * * * pg_dump -U bot_user telegram_bot_option2 | gzip > /backups/db_$(date +\%Y\%m\%d).sql.gz
```

### Database Restore

```bash
# Stop coordinator
docker-compose stop coordinator

# Restore
gunzip -c backup_20251118.sql.gz | psql -U bot_user telegram_bot_option2

# Start coordinator
docker-compose start coordinator
```

### Disaster Recovery

**RTO**: 1 hour | **RPO**: 24 hours

1. Restore database from backup
2. Deploy coordinator from Docker image
3. Verify health endpoints
4. Run crash recovery (automatic on startup)

---

## Performance Tuning

### Optimize for Higher Throughput

```bash
# .env adjustments
MAX_DOWNLOAD_WORKERS=5   # Increase from 3
MAX_STORE_WORKERS=8      # Increase from 5 (if resources allow)
BATCH_SIZE=20            # Increase from 10
```

### Optimize for Lower Resource Usage

```bash
# .env adjustments
MAX_DOWNLOAD_WORKERS=2   # Decrease from 3
MAX_STORE_WORKERS=3      # Decrease from 5
BATCH_TIMEOUT_SEC=600    # Increase from 300 (less frequent batches)
```

### Database Optimization

```sql
-- Add indexes for common queries
CREATE INDEX CONCURRENTLY idx_dq_status_created
  ON download_queue(status, created_at);

CREATE INDEX CONCURRENTLY idx_bp_status_completed
  ON batch_processing(status, completed_at);

-- Vacuum and analyze
VACUUM ANALYZE download_queue;
VACUUM ANALYZE batch_processing;
```

---

## Rollback Procedures

### Docker Compose Rollback

```bash
# Stop current version
docker-compose down

# Checkout previous version
git checkout HEAD~1

# Rebuild and start
docker-compose up -d --build
```

### Kubernetes Rollback

```bash
# Rollback to previous version
kubectl rollout undo deployment/telegram-bot-coordinator

# Check rollback status
kubectl rollout status deployment/telegram-bot-coordinator

# Verify health
kubectl exec -it $(kubectl get pod -l app=telegram-bot -o name) -- \
  wget -qO- localhost:8080/health
```

---

## Contact & Escalation

For issues not covered in this runbook:

1. Check logs: `docker-compose logs coordinator`
2. Check Grafana dashboards for trends
3. Review implementation plan documentation in Docs/
4. File issue at: https://github.com/redlabs-sc/telegram-data-processor-bot-v3/issues

---

## Appendix: Key Configuration Values

| Setting | Default | Description |
|---------|---------|-------------|
| MAX_DOWNLOAD_WORKERS | 3 | Concurrent download workers |
| MAX_EXTRACT_WORKERS | 1 | **FIXED** - Cannot change |
| MAX_CONVERT_WORKERS | 1 | **FIXED** - Cannot change |
| MAX_STORE_WORKERS | 5 | Concurrent store workers |
| BATCH_SIZE | 10 | Files per batch |
| BATCH_TIMEOUT_SEC | 300 | Seconds before partial batch |
| COMPLETED_BATCH_RETENTION_HOURS | 1 | Hours to keep completed |
| FAILED_BATCH_RETENTION_DAYS | 7 | Days to keep failed |
