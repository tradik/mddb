package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// The training manager: datasets and runs as jobs, each in a goroutine of
// its own, a per-profile limit on concurrent runs, cancel, delete, and the
// durable list (training_store.go). Datasets need no trainer; a run needs a
// profile from the operator's file.

var (
	errTrainingNoTrainer = errors.New("no trainer is configured on this server: its operator defines trainer profiles in " + trainersConfigEnv)
	errTrainingInvalid   = errors.New("invalid training request")
	errTrainingBusy      = errors.New("the profile already runs as many jobs as it allows")
	errTrainingState     = errors.New("the job is not in a state that allows this")
	errTrainingCancelled = errors.New("cancelled")
	errServerStopping    = errors.New(trainingInterruptedReason)
)

// trainingStopWait bounds how long shutdown waits for running jobs to end
// after they were told to stop.
var trainingStopWait = 30 * time.Second

// trainingManager owns the jobs of the module.
type trainingManager struct {
	s        *Server
	profiles *trainerProfiles
	root     string
	ctx      context.Context
	stopAll  context.CancelCauseFunc
	wg       sync.WaitGroup
	// mu guards running and slots, and serialises changes to a live job
	// with the write of its record.
	mu       sync.Mutex
	running  map[string]context.CancelCauseFunc
	slots    map[string]int
	lastSave map[string]time.Time
}

// newTrainingManager reads the profiles and recovers the jobs a previous
// process left running.
func newTrainingManager(s *Server) *trainingManager {
	ctx, stop := context.WithCancelCause(context.Background())
	m := &trainingManager{s: s, profiles: loadTrainerProfiles(), root: s.trainingDir(), ctx: ctx, stopAll: stop,
		running: map[string]context.CancelCauseFunc{}, slots: map[string]int{}, lastSave: map[string]time.Time{}}
	for _, e := range m.profiles.errors {
		slog.Warn("trainer profile refused", "err", e)
	}
	if err := m.recoverJobs(); err != nil {
		slog.Warn("training jobs could not be recovered", "err", err)
	}
	return m
}

// stop tells every running job to end and waits for them, within a bound.
// A job stopped so is marked interrupted.
func (m *trainingManager) stop() {
	m.stopAll(errServerStopping)
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(trainingStopWait):
		slog.Warn("training jobs still running at shutdown; the next start marks them interrupted")
	}
}

// update changes a live job and writes its record.
func (m *trainingManager) update(j *trainingJob, change func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	change()
	if err := m.saveJob(j); err != nil {
		slog.Warn("saving a training job failed", "job", j.ID, "err", err)
	}
}

// progress records a progress line, writing the record once a second.
func (m *trainingManager) progress(j *trainingJob, pct float64, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.Progress, j.ProgressMessage = &pct, msg
	if time.Since(m.lastSave[j.ID]) < time.Second {
		return
	}
	m.lastSave[j.ID] = time.Now()
	_ = m.saveJob(j)
}

// launch runs fn in the background with a context cancel and shutdown end.
func (m *trainingManager) launch(id string, fn func(ctx context.Context)) {
	ctx, cancel := context.WithCancelCause(m.ctx)
	m.mu.Lock()
	m.running[id] = cancel
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.running, id)
			delete(m.lastSave, id)
			m.mu.Unlock()
			cancel(nil)
		}()
		fn(ctx)
	}()
}

// acquireSlot takes one of a profile's concurrent runs.
func (m *trainingManager) acquireSlot(p *trainerProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.slots[p.Name] >= p.MaxConcurrent {
		return fmt.Errorf("%w (%d)", errTrainingBusy, p.MaxConcurrent)
	}
	m.slots[p.Name]++
	return nil
}

// releaseSlot gives a run's slot back.
func (m *trainingManager) releaseSlot(p *trainerProfile) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.slots[p.Name]--
}

// running reports how many runs a profile has now.
func (m *trainingManager) runningOf(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.slots[name]
}

// preparedDataset is a dataset request checked before its job starts.
type preparedDataset struct {
	req    datasetRequest
	ack    []string
	budget int64
}

// prepareDataset checks a request, its licences and the free disk space.
func (m *trainingManager) prepareDataset(req datasetRequest) (*preparedDataset, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	ack, err := m.s.checkLicences(&req)
	if err != nil {
		return nil, err
	}
	budget, err := m.diskBudget()
	if err != nil {
		return nil, err
	}
	return &preparedDataset{req: req, ack: ack, budget: budget}, nil
}

// newJob is a pending job's first record.
func (m *trainingManager) newJob(kind, name, user string) (*trainingJob, error) {
	j := &trainingJob{ID: newTrainingID(kind), Kind: kind, Name: name, Status: BulkJobPending,
		User: user, SubmittedAt: time.Now().Unix()}
	return j, m.saveJob(j)
}

// submitDataset starts building a dataset.
func (m *trainingManager) submitDataset(req datasetRequest, user string) (*trainingJob, error) {
	prep, err := m.prepareDataset(req)
	if err != nil {
		return nil, err
	}
	j, err := m.newJob(trainingKindDataset, req.Name, user)
	if err != nil {
		return nil, err
	}
	m.launch(j.ID, func(ctx context.Context) { _ = m.buildDataset(ctx, j, prep) })
	return j, nil
}

// buildDataset reads, writes and summarises a dataset, recording its end.
// A cancel is seen once the files are written: reading is not interrupted.
func (m *trainingManager) buildDataset(ctx context.Context, j *trainingJob, prep *preparedDataset) error {
	m.update(j, func() { j.Status, j.Stage, j.StartedAt = BulkJobProcessing, stageDataset, time.Now().Unix() })
	sum, err := m.writeDataset(j.ID, prep)
	if err == nil && ctx.Err() != nil {
		err = context.Cause(ctx)
	}
	m.update(j, func() {
		j.CompletedAt, j.Summary = time.Now().Unix(), sum
		j.Status = endStatus(err)
		if err != nil {
			j.Error = err.Error()
		}
	})
	return err
}

// endStatus is the status a job ends in after err.
func endStatus(err error) BulkJobStatus {
	switch {
	case err == nil:
		return BulkJobCompleted
	case errors.Is(err, errTrainingCancelled):
		return BulkJobCancelled
	case errors.Is(err, errServerStopping):
		return trainingInterrupted
	default:
		return BulkJobFailed
	}
}

// writeDataset writes the dataset's two parts into its job directory.
func (m *trainingManager) writeDataset(id string, prep *preparedDataset) (*datasetSummary, error) {
	records, sums, err := m.s.datasetRecords(&prep.req)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%w: no document gave a record (check the collections, filter, language and fields)", errDatasetEmpty)
	}
	train, validation := splitRecords(records, prep.req.ValidationRatio)
	jr, err := m.jobRoot(id, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = jr.Close() }()
	_ = jr.MkdirAll(datasetSubdir, 0o750) // a failure shows in the writes below
	n1, err1 := writeRecords(jr, datasetSubdir+"/"+trainFile, train, prep.budget)
	n2, err2 := writeRecords(jr, datasetSubdir+"/"+validationFile, validation, prep.budget-n1)
	if err := errors.Join(err1, err2); err != nil {
		return nil, err
	}
	return &datasetSummary{Format: prep.req.Format, Records: len(records), TrainRecords: len(train),
		ValidationRecords: len(validation), Bytes: n1 + n2, Collections: sums,
		UnlicensedAcknowledged: prep.ack, Preview: previewRecords(train), Options: prep.req}, nil
}

// cancel stops a pending or running job.
func (m *trainingManager) cancel(id, kind string) (*trainingJob, error) {
	j, err := m.jobOfKind(id, kind)
	if err != nil {
		return nil, err
	}
	if !j.active() {
		return nil, fmt.Errorf("%w: it is %s", errTrainingState, j.Status)
	}
	m.mu.Lock()
	stop, ok := m.running[id]
	m.mu.Unlock()
	if ok {
		stop(errTrainingCancelled)
	}
	return j, nil
}

// remove deletes a finished job, its directory and its record. A dataset a
// running job uses stays.
func (m *trainingManager) remove(id, kind string) error {
	j, err := m.jobOfKind(id, kind)
	if err != nil {
		return err
	}
	if j.active() {
		return fmt.Errorf("%w: cancel it first", errTrainingState)
	}
	if kind == trainingKindDataset {
		runs, _ := m.listJobs(trainingKindRun)
		for _, r := range runs {
			if r.DatasetID == id && r.active() {
				return fmt.Errorf("%w: run %s uses it", errTrainingState, r.ID)
			}
		}
	}
	// The record goes even when the directory could not be removed: the
	// error says so, and the files are the operator's to clear.
	return errors.Join(m.removeJobDir(id), m.deleteRecord(id))
}

// jobOfKind reads a job and checks its kind.
func (m *trainingManager) jobOfKind(id, kind string) (*trainingJob, error) {
	j, err := m.getJob(id)
	if err != nil {
		return nil, err
	}
	if j.Kind != kind {
		return nil, errTrainingNotFound
	}
	return j, nil
}
