package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Reindex jobs (#347). A synchronous reindex of tens of thousands of
// documents held one HTTP request open for an hour, past the write timeout,
// and the client got EOF though the reindex finished. With "async": true the
// request returns a job at once, and GET /v1/vector-reindex/jobs/{id} gives
// its progress and, at the end, its result. Jobs live in memory: a restart
// forgets them (the vectors already written stay).

const maxReindexJobs = 50

// vectorReindexJob is one reindex running in the background.
type vectorReindexJob struct {
	ID         string              `json:"jobId"`
	Collection string              `json:"collection"`
	Force      bool                `json:"force"`
	Status     string              `json:"status"` // running, completed, failed
	StartedAt  time.Time           `json:"startedAt"`
	FinishedAt *time.Time          `json:"finishedAt,omitempty"`
	Done       int                 `json:"done"`
	Percent    float64             `json:"percent"`
	EtaSeconds int                 `json:"etaSeconds"`
	Error      string              `json:"error,omitempty"`
	Result     vectorReindexResult `json:"result"`
}

type reindexJobs struct {
	mu   sync.Mutex
	jobs map[string]*vectorReindexJob
}

func (j *reindexJobs) get(id string) (vectorReindexJob, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.jobs[id]
	if !ok {
		return vectorReindexJob{}, false
	}
	return *job, true
}

func (j *reindexJobs) list() []vectorReindexJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]vectorReindexJob, 0, len(j.jobs))
	for _, job := range j.jobs {
		out = append(out, *job)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].StartedAt.After(out[b].StartedAt) })
	return out
}

var errReindexRunning = errors.New("a reindex of this collection is already running")

// start registers a job, unless one for the collection is running, and
// drops the oldest finished jobs past the limit.
func (j *reindexJobs) start(collection string, force bool) (*vectorReindexJob, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.jobs == nil {
		j.jobs = map[string]*vectorReindexJob{}
	}
	for _, job := range j.jobs {
		if job.Collection == collection && job.Status == "running" {
			return job, errReindexRunning
		}
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	job := &vectorReindexJob{ID: "reindex_" + hex.EncodeToString(b), Collection: collection, Force: force,
		Status: "running", StartedAt: time.Now(), EtaSeconds: -1}
	j.jobs[job.ID] = job
	for len(j.jobs) > maxReindexJobs {
		oldest := ""
		for id, jb := range j.jobs {
			if jb.Status != "running" && (oldest == "" || jb.StartedAt.Before(j.jobs[oldest].StartedAt)) {
				oldest = id
			}
		}
		if oldest == "" {
			break
		}
		delete(j.jobs, oldest)
	}
	return job, nil
}

func (j *reindexJobs) update(id string, fn func(*vectorReindexJob)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job, ok := j.jobs[id]; ok {
		fn(job)
	}
}

// startReindexJob runs a reindex in the background and returns its job.
func (s *Server) startReindexJob(collection string, force bool) (vectorReindexJob, error) {
	job, err := s.reindexJobs.start(collection, force)
	if err != nil {
		return *job, err
	}
	go func(id string) {
		res, err := s.reindexVectors(context.Background(), collection, force, func(done int, r vectorReindexResult) {
			s.reindexJobs.update(id, func(j *vectorReindexJob) {
				j.Done, j.Result = done, r
				if r.Total > 0 {
					j.Percent = float64(int(1000*float64(done)/float64(r.Total))) / 10
				}
				if el := time.Since(j.StartedAt).Seconds(); done > 0 && el > 0 {
					j.EtaSeconds = int(float64(r.Total-done) / (float64(done) / el))
				}
			})
		})
		now := time.Now()
		s.reindexJobs.update(id, func(j *vectorReindexJob) {
			j.FinishedAt, j.Result, j.EtaSeconds = &now, res, 0
			j.Status = "completed"
			if err != nil {
				j.Status, j.Error = "failed", err.Error()
			}
		})
	}(job.ID)
	return *job, nil
}

// handleVectorReindexJobs answers GET /v1/vector-reindex/jobs and
// GET /v1/vector-reindex/jobs/{id}.
func (s *Server) handleVectorReindexJobs(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/vector-reindex/jobs"), "/")
	if id == "" {
		ok(w, map[string]any{"jobs": s.reindexJobs.list()})
		return
	}
	job, found := s.reindexJobs.get(id)
	if !found {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such reindex job (jobs are kept in memory and a restart forgets them)"}`))
		return
	}
	ok(w, job)
}
