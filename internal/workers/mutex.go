package workers

import (
	"sync"
)

// Global mutexes to enforce single-instance constraints
// CRITICAL: Extract and convert cannot run simultaneously
var (
	ExtractMutex sync.Mutex
	ConvertMutex sync.Mutex
)

// Stage status constants (corrected architecture)
const (
	StatusQueuedExtract = "QUEUED_EXTRACT"
	StatusExtracting    = "EXTRACTING"
	StatusQueuedConvert = "QUEUED_CONVERT"
	StatusConverting    = "CONVERTING"
	StatusQueuedStore   = "QUEUED_STORE"
	StatusStoring       = "STORING"
	StatusCompleted     = "COMPLETED"
	StatusFailedExtract = "FAILED_EXTRACT"
	StatusFailedConvert = "FAILED_CONVERT"
	StatusFailedStore   = "FAILED_STORE"
)
