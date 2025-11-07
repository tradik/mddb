package main

import (
	"context"
	"log"
	"sync"

	bolt "go.etcd.io/bbolt"
)

// IndexJob represents a metadata indexing job
type IndexJob struct {
	Collection string
	DocID      string
	OldMeta    map[string][]string
	NewMeta    map[string][]string
}

// IndexQueue manages asynchronous metadata indexing
type IndexQueue struct {
	server     *Server
	queue      chan *IndexJob
	workers    int
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	processed  uint64
	failed     uint64
	mu         sync.RWMutex
}

// NewIndexQueue creates a new index queue
func NewIndexQueue(server *Server, workers int) *IndexQueue {
	if workers <= 0 {
		workers = 4 // Default 4 workers
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	iq := &IndexQueue{
		server:  server,
		queue:   make(chan *IndexJob, 1000), // Buffer 1000 jobs
		workers: workers,
		ctx:     ctx,
		cancel:  cancel,
	}
	
	// Start workers
	for i := 0; i < workers; i++ {
		iq.wg.Add(1)
		go iq.worker(i)
	}
	
	return iq
}

// Enqueue adds an indexing job to the queue
func (iq *IndexQueue) Enqueue(job *IndexJob) {
	select {
	case iq.queue <- job:
		// Job queued successfully
	case <-iq.ctx.Done():
		// Queue is shutting down
	default:
		// Queue is full, log warning
		log.Printf("Index queue full, dropping job for doc %s", job.DocID)
	}
}

// worker processes indexing jobs
func (iq *IndexQueue) worker(id int) {
	defer iq.wg.Done()
	
	for {
		select {
		case job := <-iq.queue:
			if err := iq.processJob(job); err != nil {
				log.Printf("Worker %d: failed to index doc %s: %v", id, job.DocID, err)
				iq.mu.Lock()
				iq.failed++
				iq.mu.Unlock()
			} else {
				iq.mu.Lock()
				iq.processed++
				iq.mu.Unlock()
			}
		case <-iq.ctx.Done():
			return
		}
	}
}

// processJob processes a single indexing job
func (iq *IndexQueue) processJob(job *IndexJob) error {
	return iq.server.DB.Update(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(iq.server.BucketNames.IdxMeta)
		
		// Delete old indices
		if job.OldMeta != nil {
			for mk, vals := range job.OldMeta {
				for _, mv := range vals {
					key := kMetaKeyPrefix(job.Collection, mk, mv)
					key = append(key, []byte(job.DocID)...)
					_ = bIdx.Delete(key)
				}
			}
		}
		
		// Add new indices
		if job.NewMeta != nil {
			for mk, vals := range job.NewMeta {
				for _, mv := range vals {
					key := kMetaKeyPrefix(job.Collection, mk, mv)
					key = append(key, []byte(job.DocID)...)
					if err := bIdx.Put(key, []byte("1")); err != nil {
						return err
					}
				}
			}
		}
		
		return nil
	})
}

// Shutdown gracefully shuts down the index queue
func (iq *IndexQueue) Shutdown() {
	iq.cancel()
	iq.wg.Wait()
	close(iq.queue)
}

// Stats returns queue statistics
func (iq *IndexQueue) Stats() (processed, failed uint64, queueLen int) {
	iq.mu.RLock()
	defer iq.mu.RUnlock()
	return iq.processed, iq.failed, len(iq.queue)
}
