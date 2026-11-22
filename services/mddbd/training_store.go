package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// The durable list of training jobs: datasets and runs, one BoltDB record
// each, so status, parameters, times, exit code, result file and licence
// summary outlive a restart. The statuses are the bulk-ingest job's, plus
// two of this module: interrupted (the server stopped while the job ran) and
// completedWithWarnings (a run whose result is there but whose evaluation
// failed).

var bucketTrainingJobs = []byte("training_jobs")

// Job kinds and the statuses added to BulkJobStatus.
const (
	trainingKindDataset                     = "dataset"
	trainingKindRun                         = "run"
	trainingInterrupted       BulkJobStatus = "interrupted"
	trainingDoneWithWarnings  BulkJobStatus = "completedWithWarnings"
	trainingDirEnv                          = "MDDB_TRAINING_DIR"
	trainingInterruptedReason               = "the server stopped while the job ran"
)

// Stages of a run.
const (
	stageDataset        = "dataset"
	stageTrial          = "trial"
	stageEvaluateBase   = "evaluate-base"
	stageTraining       = "training"
	stageEvaluateResult = "evaluate-result"
)

var (
	errTrainingNotFound = errors.New("training job not found")
	trainingIDPattern   = regexp.MustCompile(`^(ds|run)_[0-9a-f]{16}$`)
)

// trainingJob is one dataset or run.
type trainingJob struct {
	ID              string              `json:"id"`
	Kind            string              `json:"kind"`
	Name            string              `json:"name,omitempty"`
	Status          BulkJobStatus       `json:"status"`
	Stage           string              `json:"stage,omitempty"`
	Profile         string              `json:"profile,omitempty"`
	Parameters      map[string]string   `json:"parameters,omitempty"`
	DatasetID       string              `json:"datasetId,omitempty"`
	Trial           bool                `json:"trial,omitempty"`
	Progress        *float64            `json:"progress,omitempty"`
	ProgressMessage string              `json:"progressMessage,omitempty"`
	User            string              `json:"user,omitempty"`
	SubmittedAt     int64               `json:"submittedAt"`
	StartedAt       int64               `json:"startedAt,omitempty"`
	CompletedAt     int64               `json:"completedAt,omitempty"`
	ExitCode        *int                `json:"exitCode,omitempty"`
	Error           string              `json:"error,omitempty"`
	Warnings        []string            `json:"warnings,omitempty"`
	Result          *trainingResult     `json:"result,omitempty"`
	OutputPattern   string              `json:"outputPattern,omitempty"`
	Summary         *datasetSummary     `json:"summary,omitempty"`
	Evaluation      *trainingEvaluation `json:"evaluation,omitempty"`
	// Evaluate and EvalParameters are what the run asked for, kept for a
	// later re-evaluation.
	Evaluate       *bool          `json:"evaluate,omitempty"`
	EvalParameters map[string]any `json:"evaluateParameters,omitempty"`
	// Format and ValidationRecords are the dataset's as the run used it
	// (a trial's cut), which decide whether it can be evaluated.
	Format            string `json:"format,omitempty"`
	ValidationRecords int    `json:"validationRecords,omitempty"`
}

// trainingResult is the file a run produced, inside its output directory.
type trainingResult struct {
	File  string `json:"file"`
	Bytes int64  `json:"bytes"`
}

// active reports a job that has not ended.
func (j *trainingJob) active() bool {
	return j.Status == BulkJobPending || j.Status == BulkJobProcessing
}

// newTrainingID is a random job id with its kind's prefix; the pattern it
// matches is what a request's id is checked against before it names a
// directory.
func newTrainingID(kind string) string {
	prefix := "run_"
	if kind == trainingKindDataset {
		prefix = "ds_"
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// trainingDir is MDDB_TRAINING_DIR, or "training" beside the database, as
// an absolute path: a command runs in its job directory, so a relative path
// handed to it would point somewhere else. (Abs fails only without a working
// directory; the empty root then fails every job with a clear error.)
func (s *Server) trainingDir() string {
	dir := os.Getenv(trainingDirEnv)
	switch {
	case dir != "":
	case s.Path == "":
		dir = filepath.Join(os.TempDir(), "mddb-training")
	default:
		dir = filepath.Join(filepath.Dir(s.Path), "training")
	}
	abs, _ := filepath.Abs(dir)
	return abs
}

// writeBucket runs fn on the job bucket in a write transaction, creating
// the bucket: a restored database may not have it.
func (m *trainingManager) writeBucket(fn func(b *bolt.Bucket) error) error {
	return m.s.DBUpdate(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketTrainingJobs)
		if err == nil {
			err = fn(b)
		}
		return err
	})
}

// saveJob writes a job's record.
func (m *trainingManager) saveJob(j *trainingJob) error {
	buf, _ := json.Marshal(j) // plain data: it cannot fail
	return m.writeBucket(func(b *bolt.Bucket) error { return b.Put([]byte(j.ID), buf) })
}

// getJob reads a job's record.
func (m *trainingManager) getJob(id string) (*trainingJob, error) {
	if !trainingIDPattern.MatchString(id) {
		return nil, errTrainingNotFound
	}
	var j *trainingJob
	err := m.s.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTrainingJobs)
		if b == nil {
			return errTrainingNotFound
		}
		raw := b.Get([]byte(id))
		if raw == nil {
			return errTrainingNotFound
		}
		j = &trainingJob{}
		return json.Unmarshal(raw, j)
	})
	return j, err
}

// listJobs is every job of a kind, newest first.
func (m *trainingManager) listJobs(kind string) ([]*trainingJob, error) {
	out := []*trainingJob{}
	err := m.s.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTrainingJobs)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			j := &trainingJob{}
			if json.Unmarshal(v, j) == nil && (kind == "" || j.Kind == kind) {
				out = append(out, j)
			}
			return nil
		})
	})
	sort.Slice(out, func(a, b int) bool {
		if out[a].SubmittedAt != out[b].SubmittedAt {
			return out[a].SubmittedAt > out[b].SubmittedAt
		}
		return out[a].ID > out[b].ID
	})
	return out, err
}

// deleteRecord removes a job's record.
func (m *trainingManager) deleteRecord(id string) error {
	return m.writeBucket(func(b *bolt.Bucket) error { return b.Delete([]byte(id)) })
}

// recoverJobs marks every job a previous process left pending or running as
// interrupted: its process died with that server, or was stopped by it.
func (m *trainingManager) recoverJobs() error {
	jobs, err := m.listJobs("")
	for _, j := range jobs {
		if j.active() {
			j.Status, j.Error, j.CompletedAt = trainingInterrupted, trainingInterruptedReason, time.Now().Unix()
			err = errors.Join(err, m.saveJob(j))
		}
	}
	return err
}
