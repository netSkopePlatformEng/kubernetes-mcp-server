package jobs

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// JobState represents the current state of a job
type JobState string

const (
	JobStateQueued    JobState = "queued"
	JobStateRunning   JobState = "running"
	JobStateSucceeded JobState = "succeeded"
	JobStateFailed    JobState = "failed"
	JobStatePartial   JobState = "partial"
	JobStateCancelled JobState = "cancelled"
)

// JobType represents the type of job
type JobType string

const (
	JobTypeRancherDownload JobType = "rancher_download"
	JobTypeClusterExec     JobType = "cluster_exec"
)

// Job represents a long-running background job
type Job struct {
	ID             string
	Type           JobType
	State          JobState
	Progress       JobProgress
	StartedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
	IdempotencyKey string
	Params         map[string]interface{}
	CancelFunc     context.CancelFunc
	mu             sync.RWMutex
}

// JobProgress tracks the progress of a job
type JobProgress struct {
	Done       int
	Total      int
	Percentage float64
	Message    string
	ETASeconds int
}

// JobResult represents the result of processing a single item in a job
type JobResult struct {
	Cluster    string            `json:"cluster"`
	Success    bool              `json:"success"`
	DurationMs int64             `json:"duration_ms"`
	Result     interface{}       `json:"result,omitempty"`
	Error      string            `json:"error,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// JobMetadata stores summary information about a job
type JobMetadata struct {
	JobID       string      `json:"job_id"`
	Type        JobType     `json:"type"`
	State       JobState    `json:"state"`
	Progress    JobProgress `json:"progress"`
	StartedAt   time.Time   `json:"started_at"`
	CompletedAt *time.Time  `json:"completed_at,omitempty"`
	TotalItems  int         `json:"total_items"`
	Succeeded   int         `json:"succeeded"`
	Failed      int         `json:"failed"`
}

// ResultsPage represents a paginated set of job results
type ResultsPage struct {
	JobID      string         `json:"job_id"`
	State      JobState       `json:"state"`
	Items      []JobResult    `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	PageNum    int            `json:"page_num"`
	TotalItems int            `json:"total_items"`
	Summary    ResultsSummary `json:"summary"`
}

// ResultsSummary provides aggregate statistics for job results
type ResultsSummary struct {
	Total           int   `json:"total"`
	Succeeded       int   `json:"succeeded"`
	Failed          int   `json:"failed"`
	TotalDurationMs int64 `json:"total_duration_ms"`
}

// JobExecutor defines the interface for executing jobs
type JobExecutor interface {
	Execute(
		ctx context.Context,
		params map[string]interface{},
		resultsChan chan<- JobResult,
		progressChan chan<- JobProgress,
	) error
}

// NewJobID generates a new unique job ID
func NewJobID() string {
	return uuid.NewString()
}

// UpdateState updates the job state in a thread-safe manner
func (j *Job) UpdateState(state JobState) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.State = state
	j.UpdatedAt = time.Now().UTC()

	if state == JobStateSucceeded || state == JobStateFailed ||
		state == JobStatePartial || state == JobStateCancelled {
		now := time.Now().UTC()
		j.CompletedAt = &now
	}
}

// UpdateProgress updates the job progress in a thread-safe manner
func (j *Job) UpdateProgress(progress JobProgress) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.Progress = progress
	j.UpdatedAt = time.Now().UTC()

	// Calculate ETA if we have progress
	if progress.Done > 0 && progress.Total > 0 {
		elapsed := time.Since(j.StartedAt).Seconds()
		avgPerItem := elapsed / float64(progress.Done)
		remaining := progress.Total - progress.Done
		j.Progress.ETASeconds = int(avgPerItem * float64(remaining))
	}
}

// GetState returns the current job state in a thread-safe manner
func (j *Job) GetState() JobState {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.State
}

// GetProgress returns the current job progress in a thread-safe manner
func (j *Job) GetProgress() JobProgress {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.Progress
}

// Cancel cancels the job if it has a cancel function
func (j *Job) Cancel() {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.CancelFunc != nil {
		j.CancelFunc()
	}
	j.State = JobStateCancelled
	now := time.Now().UTC()
	j.CompletedAt = &now
}
