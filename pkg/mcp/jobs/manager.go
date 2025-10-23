package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// Manager manages the lifecycle of background jobs
type Manager struct {
	jobs          map[string]*Job
	idempotency   map[string]string // idempotency key -> job ID
	Storage       Storage           // Exported for access by MCP tools
	workerPool    *WorkerPool
	mu            sync.RWMutex
	logger        klog.Logger
	maxJobs       int
	cleanupAfter  time.Duration
	cleanupTicker *time.Ticker
	stopChan      chan struct{}
}

// ManagerConfig configures the JobManager
type ManagerConfig struct {
	StorageDir   string
	MaxJobs      int
	WorkerCount  int
	CleanupAfter time.Duration
}

// DefaultManagerConfig returns default configuration
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		StorageDir:   "~/.mcp/jobs",
		MaxJobs:      100,
		WorkerCount:  5,
		CleanupAfter: 24 * time.Hour,
	}
}

// NewManager creates a new job manager
func NewManager(config ManagerConfig) (*Manager, error) {
	// Expand home directory in storage path
	storageDir := expandPath(config.StorageDir)

	// Create storage backend
	storage, err := NewFileStorage(storageDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	// Create worker pool
	workerPool := NewWorkerPool(config.WorkerCount)

	m := &Manager{
		jobs:         make(map[string]*Job),
		idempotency:  make(map[string]string),
		Storage:      storage,
		workerPool:   workerPool,
		logger:       klog.Background(),
		maxJobs:      config.MaxJobs,
		cleanupAfter: config.CleanupAfter,
		stopChan:     make(chan struct{}),
	}

	// Start cleanup goroutine
	m.cleanupTicker = time.NewTicker(1 * time.Hour)
	go m.cleanupLoop()

	m.logger.Info("JobManager created", "storageDir", storageDir, "maxJobs", config.MaxJobs, "workers", config.WorkerCount)

	return m, nil
}

// CreateOrGet creates a new job or returns an existing one if idempotency key matches
func (m *Manager) CreateOrGet(idempotencyKey string, jobType JobType, params map[string]interface{}) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check idempotency
	if idempotencyKey != "" {
		if existingJobID, exists := m.idempotency[idempotencyKey]; exists {
			if job, found := m.jobs[existingJobID]; found {
				m.logger.V(2).Info("Returning existing job for idempotency key",
					"key", idempotencyKey, "jobID", existingJobID)
				return job, false // not created
			}
		}
	}

	// Check if we've hit max jobs
	if len(m.jobs) >= m.maxJobs {
		// Try to clean up old jobs
		m.cleanupOldJobsLocked()
	}

	// Create new job
	job := &Job{
		ID:             NewJobID(),
		Type:           jobType,
		State:          JobStateQueued,
		StartedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		IdempotencyKey: idempotencyKey,
		Params:         params,
		Progress:       JobProgress{},
	}

	m.jobs[job.ID] = job
	if idempotencyKey != "" {
		m.idempotency[idempotencyKey] = job.ID
	}

	m.logger.Info("Created new job", "jobID", job.ID, "type", jobType)
	return job, true // created
}

// StartJob starts executing a job with the given executor
func (m *Manager) StartJob(jobID string, executor JobExecutor) error {
	job := m.GetJob(jobID)
	if job == nil {
		return fmt.Errorf("job not found: %s", jobID)
	}

	// Create cancelable context
	ctx, cancel := context.WithCancel(context.Background())
	job.CancelFunc = cancel

	// Submit to worker pool
	err := m.workerPool.Submit(func() {
		m.executeJob(ctx, job, executor)
	})

	if err != nil {
		return fmt.Errorf("failed to submit job to worker pool: %w", err)
	}

	return nil
}

// executeJob executes a job with the given executor
func (m *Manager) executeJob(ctx context.Context, job *Job, executor JobExecutor) {
	job.UpdateState(JobStateRunning)

	resultsChan := make(chan JobResult, 100)
	progressChan := make(chan JobProgress, 10)

	// Start goroutines to handle results and progress
	var wg sync.WaitGroup

	// Results writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.writeResults(ctx, job, resultsChan)
	}()

	// Progress updater goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for progress := range progressChan {
			job.UpdateProgress(progress)
		}
	}()

	// Execute the job
	m.logger.Info("Executing job", "jobID", job.ID, "type", job.Type)
	err := executor.Execute(ctx, job.Params, resultsChan, progressChan)

	// Close channels to signal completion
	close(resultsChan)
	close(progressChan)

	// Wait for result/progress handlers to finish
	wg.Wait()

	// Update final state
	if err != nil {
		m.logger.Error(err, "Job execution failed", "jobID", job.ID)
		job.UpdateState(JobStateFailed)
	} else {
		progress := job.GetProgress()
		if progress.Total > 0 && progress.Done < progress.Total {
			job.UpdateState(JobStatePartial)
		} else {
			job.UpdateState(JobStateSucceeded)
		}
	}

	// Save final metadata
	m.saveJobMetadata(job)

	m.logger.Info("Job execution completed", "jobID", job.ID, "state", job.GetState())
}

// writeResults writes job results to storage
func (m *Manager) writeResults(ctx context.Context, job *Job, resultsChan <-chan JobResult) {
	succeeded := 0
	failed := 0

	for result := range resultsChan {
		// Write to storage
		if err := m.Storage.AppendResult(job.ID, result); err != nil {
			m.logger.Error(err, "Failed to write result", "jobID", job.ID, "cluster", result.Cluster)
		}

		// Update counters
		if result.Success {
			succeeded++
		} else {
			failed++
		}

		// Periodically save metadata
		if (succeeded+failed)%10 == 0 {
			meta := &JobMetadata{
				JobID:     job.ID,
				Type:      job.Type,
				State:     job.GetState(),
				Progress:  job.GetProgress(),
				StartedAt: job.StartedAt,
				Succeeded: succeeded,
				Failed:    failed,
			}
			m.Storage.SaveJobMetadata(job.ID, meta)
		}
	}

	// Save final counts
	meta := &JobMetadata{
		JobID:       job.ID,
		Type:        job.Type,
		State:       job.GetState(),
		Progress:    job.GetProgress(),
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
		Succeeded:   succeeded,
		Failed:      failed,
		TotalItems:  succeeded + failed,
	}
	m.Storage.SaveJobMetadata(job.ID, meta)
}

// saveJobMetadata saves job metadata to storage
func (m *Manager) saveJobMetadata(job *Job) {
	meta, _ := m.Storage.GetJobMetadata(job.ID)
	if meta == nil {
		meta = &JobMetadata{}
	}

	meta.JobID = job.ID
	meta.Type = job.Type
	meta.State = job.GetState()
	meta.Progress = job.GetProgress()
	meta.StartedAt = job.StartedAt
	meta.CompletedAt = job.CompletedAt

	if err := m.Storage.SaveJobMetadata(job.ID, meta); err != nil {
		m.logger.Error(err, "Failed to save job metadata", "jobID", job.ID)
	}
}

// GetJob retrieves a job by ID
func (m *Manager) GetJob(jobID string) *Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobs[jobID]
}

// CancelJob cancels a running job
func (m *Manager) CancelJob(jobID string) error {
	job := m.GetJob(jobID)
	if job == nil {
		return fmt.Errorf("job not found: %s", jobID)
	}

	job.Cancel()
	m.logger.Info("Job cancelled", "jobID", jobID)
	return nil
}

// ListJobs returns all active jobs
func (m *Manager) ListJobs() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	jobs := make([]*Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

// cleanupLoop periodically cleans up old jobs
func (m *Manager) cleanupLoop() {
	for {
		select {
		case <-m.stopChan:
			m.cleanupTicker.Stop()
			return
		case <-m.cleanupTicker.C:
			m.cleanupOldJobs()
		}
	}
}

// cleanupOldJobs removes jobs older than cleanupAfter
func (m *Manager) cleanupOldJobs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupOldJobsLocked()
}

// cleanupOldJobsLocked removes old jobs (must be called with lock held)
func (m *Manager) cleanupOldJobsLocked() {
	now := time.Now()
	removed := 0
	failed := 0

	for jobID, job := range m.jobs {
		// Only clean up completed jobs
		if job.CompletedAt == nil {
			continue
		}

		age := now.Sub(*job.CompletedAt)
		if age > m.cleanupAfter {
			// Remove from memory
			delete(m.jobs, jobID)
			if job.IdempotencyKey != "" {
				delete(m.idempotency, job.IdempotencyKey)
			}

			// Remove from storage (files)
			if err := m.Storage.DeleteJob(jobID); err != nil {
				m.logger.Error(err, "Failed to delete job from storage", "jobID", jobID)
				failed++
			} else {
				removed++
			}
		}
	}

	if removed > 0 || failed > 0 {
		m.logger.Info("Cleaned up old jobs", "removed", removed, "failed", failed)
	}
}

// Shutdown gracefully shuts down the job manager
func (m *Manager) Shutdown() {
	m.logger.Info("Shutting down JobManager")

	// Stop cleanup loop
	close(m.stopChan)

	// Cancel all running jobs
	m.mu.Lock()
	for _, job := range m.jobs {
		if job.CancelFunc != nil {
			job.Cancel()
		}
	}
	m.mu.Unlock()

	// Shutdown worker pool
	m.workerPool.Shutdown()

	m.logger.Info("JobManager shut down complete")
}

// expandPath expands ~ in paths
func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home := os.Getenv("HOME")
		if home == "" {
			home = os.Getenv("USERPROFILE") // Windows
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
