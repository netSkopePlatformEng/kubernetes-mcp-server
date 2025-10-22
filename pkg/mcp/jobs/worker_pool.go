package jobs

import (
	"context"
	"sync"

	"k8s.io/klog/v2"
)

// WorkerPool manages a pool of workers for executing jobs
type WorkerPool struct {
	workerCount int
	queue       chan func()
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	logger      klog.Logger
}

// NewWorkerPool creates a new worker pool with the specified number of workers
func NewWorkerPool(workerCount int) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())

	wp := &WorkerPool{
		workerCount: workerCount,
		queue:       make(chan func(), 100), // Buffered channel for queuing
		ctx:         ctx,
		cancel:      cancel,
		logger:      klog.Background(),
	}

	// Start workers
	for i := 0; i < workerCount; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}

	wp.logger.V(2).Info("WorkerPool started", "workers", workerCount)
	return wp
}

// worker processes jobs from the queue
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	wp.logger.V(3).Info("Worker started", "id", id)

	for {
		select {
		case <-wp.ctx.Done():
			wp.logger.V(3).Info("Worker stopping", "id", id)
			return
		case job, ok := <-wp.queue:
			if !ok {
				wp.logger.V(3).Info("Worker stopping (queue closed)", "id", id)
				return
			}
			wp.logger.V(4).Info("Worker executing job", "id", id)
			job()
		}
	}
}

// Submit submits a job to the worker pool
func (wp *WorkerPool) Submit(job func()) error {
	select {
	case <-wp.ctx.Done():
		return context.Canceled
	case wp.queue <- job:
		return nil
	}
}

// Shutdown gracefully shuts down the worker pool
func (wp *WorkerPool) Shutdown() {
	wp.logger.V(2).Info("Shutting down WorkerPool")

	// Stop accepting new jobs
	wp.cancel()

	// Close the queue to signal workers
	close(wp.queue)

	// Wait for all workers to finish
	wp.wg.Wait()

	wp.logger.V(2).Info("WorkerPool shut down complete")
}

// QueueSize returns the current number of jobs in the queue
func (wp *WorkerPool) QueueSize() int {
	return len(wp.queue)
}

// IsFull returns true if the queue is full
func (wp *WorkerPool) IsFull() bool {
	return len(wp.queue) == cap(wp.queue)
}
