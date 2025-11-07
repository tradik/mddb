package main

import (
	"context"
	"sync"
	"time"

	proto "mddb/proto"
)

// WorkerPool manages a pool of workers for processing requests
type WorkerPool struct {
	workers   int
	jobs      chan *Job
	results   chan *JobResult
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	grpcServer *GRPCServer
}

// Job represents a work item
type Job struct {
	Type    string // "add", "get", "search"
	Request interface{}
	Context context.Context
}

// JobResult represents the result of a job
type JobResult struct {
	Response interface{}
	Error    error
}

// NewWorkerPool creates a new worker pool
func NewWorkerPool(workers int, grpcServer *GRPCServer) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	
	pool := &WorkerPool{
		workers:    workers,
		jobs:       make(chan *Job, workers*2), // Buffer = 2x workers
		results:    make(chan *JobResult, workers*2),
		ctx:        ctx,
		cancel:     cancel,
		grpcServer: grpcServer,
	}
	
	// Start workers
	for i := 0; i < workers; i++ {
		pool.wg.Add(1)
		go pool.worker(i)
	}
	
	return pool
}

// worker processes jobs from the queue
func (p *WorkerPool) worker(id int) {
	defer p.wg.Done()
	
	for {
		select {
		case <-p.ctx.Done():
			return
		case job, ok := <-p.jobs:
			if !ok {
				return
			}
			
			// Process job
			result := p.processJob(job)
			
			// Send result
			select {
			case p.results <- result:
			case <-time.After(5 * time.Second):
				// Timeout sending result
			}
		}
	}
}

// processJob processes a single job
func (p *WorkerPool) processJob(job *Job) *JobResult {
	switch job.Type {
	case "add":
		req := job.Request.(*proto.AddRequest)
		resp, err := p.grpcServer.Add(job.Context, req)
		return &JobResult{Response: resp, Error: err}
		
	case "get":
		req := job.Request.(*proto.GetRequest)
		resp, err := p.grpcServer.Get(job.Context, req)
		return &JobResult{Response: resp, Error: err}
		
	case "search":
		req := job.Request.(*proto.SearchRequest)
		resp, err := p.grpcServer.Search(job.Context, req)
		return &JobResult{Response: resp, Error: err}
		
	default:
		return &JobResult{Error: ErrUnknownJobType}
	}
}

// Submit submits a job to the pool
func (p *WorkerPool) Submit(job *Job) error {
	select {
	case p.jobs <- job:
		return nil
	case <-time.After(5 * time.Second):
		return ErrJobQueueFull
	case <-p.ctx.Done():
		return ErrPoolClosed
	}
}

// GetResult gets a result from the pool
func (p *WorkerPool) GetResult() (*JobResult, error) {
	select {
	case result := <-p.results:
		return result, nil
	case <-time.After(10 * time.Second):
		return nil, ErrResultTimeout
	case <-p.ctx.Done():
		return nil, ErrPoolClosed
	}
}

// Close closes the worker pool
func (p *WorkerPool) Close() {
	p.cancel()
	close(p.jobs)
	p.wg.Wait()
	close(p.results)
}

// Errors
var (
	ErrUnknownJobType = &JobError{Message: "unknown job type"}
	ErrJobQueueFull   = &JobError{Message: "job queue full"}
	ErrPoolClosed     = &JobError{Message: "pool closed"}
	ErrResultTimeout  = &JobError{Message: "result timeout"}
)

type JobError struct {
	Message string
}

func (e *JobError) Error() string {
	return e.Message
}
