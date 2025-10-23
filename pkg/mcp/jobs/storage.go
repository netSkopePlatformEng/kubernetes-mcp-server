package jobs

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

// Storage defines the interface for job result persistence
type Storage interface {
	AppendResult(jobID string, result JobResult) error
	GetResults(jobID string, cursor string, pageSize int) (*ResultsPage, error)
	SaveJobMetadata(jobID string, meta *JobMetadata) error
	GetJobMetadata(jobID string) (*JobMetadata, error)
	DeleteJob(jobID string) error
}

// FileStorage implements Storage using NDJSON files
type FileStorage struct {
	baseDir string
	mu      sync.RWMutex
	logger  klog.Logger
}

// NewFileStorage creates a new file-based storage backend
func NewFileStorage(baseDir string) (*FileStorage, error) {
	// Create base directory if it doesn't exist
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	return &FileStorage{
		baseDir: baseDir,
		logger:  klog.Background(),
	}, nil
}

// AppendResult appends a result to the job's NDJSON file
func (fs *FileStorage) AppendResult(jobID string, result JobResult) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	path := fs.resultsPath(jobID)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open results file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	if err := enc.Encode(result); err != nil {
		return fmt.Errorf("failed to encode result: %w", err)
	}

	return nil
}

// GetResults retrieves a paginated set of results
func (fs *FileStorage) GetResults(jobID string, cursor string, pageSize int) (*ResultsPage, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	// Parse cursor (base64 encoded line number)
	startLine := 0
	if cursor != "" {
		decoded, err := base64.StdEncoding.DecodeString(cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor: %w", err)
		}
		startLine, err = strconv.Atoi(string(decoded))
		if err != nil {
			return nil, fmt.Errorf("invalid cursor format: %w", err)
		}
	}

	// Read results file
	path := fs.resultsPath(jobID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No results yet - return empty page
			meta, _ := fs.GetJobMetadata(jobID)
			state := JobStateQueued
			if meta != nil {
				state = meta.State
			}
			return &ResultsPage{
				JobID:   jobID,
				State:   state,
				Items:   []JobResult{},
				PageNum: 1,
			}, nil
		}
		return nil, fmt.Errorf("failed to open results file: %w", err)
	}
	defer f.Close()

	// Scan file and collect results
	scanner := bufio.NewScanner(f)
	lineNum := 0
	var items []JobResult
	totalItems := 0

	// Count total items first
	for scanner.Scan() {
		totalItems++
	}

	// Rewind and read the page
	f.Seek(0, 0)
	scanner = bufio.NewScanner(f)

	for scanner.Scan() {
		if lineNum >= startLine && len(items) < pageSize {
			var result JobResult
			if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
				fs.logger.Error(err, "failed to unmarshal result", "line", lineNum)
				lineNum++
				continue
			}
			items = append(items, result)
		}
		lineNum++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading results: %w", err)
	}

	// Calculate next cursor
	var nextCursor string
	nextLine := startLine + len(items)
	if nextLine < totalItems {
		nextCursor = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(nextLine)))
	}

	// Get job metadata for state and summary
	meta, err := fs.GetJobMetadata(jobID)
	state := JobStateRunning
	summary := ResultsSummary{Total: totalItems}
	if meta != nil {
		state = meta.State
		summary.Succeeded = meta.Succeeded
		summary.Failed = meta.Failed
	}

	// Calculate total duration from items
	var totalDuration int64
	for _, item := range items {
		totalDuration += item.DurationMs
	}
	summary.TotalDurationMs = totalDuration

	pageNum := (startLine / pageSize) + 1

	return &ResultsPage{
		JobID:      jobID,
		State:      state,
		Items:      items,
		NextCursor: nextCursor,
		PageNum:    pageNum,
		TotalItems: totalItems,
		Summary:    summary,
	}, nil
}

// SaveJobMetadata saves job metadata to a JSON file
func (fs *FileStorage) SaveJobMetadata(jobID string, meta *JobMetadata) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	path := fs.metadataPath(jobID)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create metadata file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(meta); err != nil {
		return fmt.Errorf("failed to encode metadata: %w", err)
	}

	return nil
}

// GetJobMetadata retrieves job metadata
func (fs *FileStorage) GetJobMetadata(jobID string) (*JobMetadata, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	path := fs.metadataPath(jobID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Not found, not an error
		}
		return nil, fmt.Errorf("failed to open metadata file: %w", err)
	}
	defer f.Close()

	var meta JobMetadata
	dec := json.NewDecoder(f)
	if err := dec.Decode(&meta); err != nil {
		return nil, fmt.Errorf("failed to decode metadata: %w", err)
	}

	return &meta, nil
}

// DeleteJob removes all files associated with a job
func (fs *FileStorage) DeleteJob(jobID string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Delete results file
	resultsPath := fs.resultsPath(jobID)
	if err := os.Remove(resultsPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete results file: %w", err)
	}

	// Delete metadata file
	metaPath := fs.metadataPath(jobID)
	if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete metadata file: %w", err)
	}

	return nil
}

// resultsPath returns the path to the results NDJSON file for a job
func (fs *FileStorage) resultsPath(jobID string) string {
	return filepath.Join(fs.baseDir, fmt.Sprintf("%s.ndjson", jobID))
}

// metadataPath returns the path to the metadata JSON file for a job
func (fs *FileStorage) metadataPath(jobID string) string {
	return filepath.Join(fs.baseDir, fmt.Sprintf("%s.meta.json", jobID))
}

// ListJobs returns a list of all job IDs in storage
func (fs *FileStorage) ListJobs() ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	entries, err := os.ReadDir(fs.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read storage directory: %w", err)
	}

	var jobIDs []string
	seen := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Extract job ID from filename (remove .ndjson or .meta.json suffix)
		if strings.HasSuffix(name, ".ndjson") {
			jobID := strings.TrimSuffix(name, ".ndjson")
			if !seen[jobID] {
				jobIDs = append(jobIDs, jobID)
				seen[jobID] = true
			}
		} else if strings.HasSuffix(name, ".meta.json") {
			jobID := strings.TrimSuffix(name, ".meta.json")
			if !seen[jobID] {
				jobIDs = append(jobIDs, jobID)
				seen[jobID] = true
			}
		}
	}

	return jobIDs, nil
}
