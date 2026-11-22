package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The failure paths of the module: a closed store, a training root that
// cannot be a directory, a full disk, files gone from a job directory.

func TestTrainingStoreAndRecovery(t *testing.T) {
	f := newTrainingFixture(t, "")
	if jobs, err := f.m.listJobs(""); err != nil || len(jobs) != 0 {
		t.Fatalf("empty store: %v %v", jobs, err)
	}
	for _, j := range []*trainingJob{
		{ID: "run_0000000000000001", Kind: trainingKindRun, Status: BulkJobProcessing, SubmittedAt: 5},
		{ID: "run_0000000000000002", Kind: trainingKindRun, Status: BulkJobCompleted, SubmittedAt: 5},
		{ID: "ds_0000000000000003", Kind: trainingKindDataset, Status: BulkJobPending, SubmittedAt: 9},
	} {
		if err := f.m.saveJob(j); err != nil {
			t.Fatal(err)
		}
	}
	jobs, _ := f.m.listJobs("")
	if len(jobs) != 3 || jobs[0].SubmittedAt != 9 || jobs[1].ID != "run_0000000000000002" {
		t.Fatalf("order: %v %v %v", jobs[0].ID, jobs[1].ID, jobs[2].ID)
	}
	// A new manager marks what was left running as interrupted.
	m := newTrainingManager(f.s)
	for _, id := range []string{"run_0000000000000001", "ds_0000000000000003"} {
		if j, _ := m.getJob(id); j.Status != trainingInterrupted || j.Error != trainingInterruptedReason {
			t.Errorf("%s after a restart: %+v", id, j)
		}
	}
	if j, _ := m.getJob("run_0000000000000002"); j.Status != BulkJobCompleted {
		t.Errorf("a finished job changed: %+v", j)
	}
	if _, err := m.getJob("../x"); !errors.Is(err, errTrainingNotFound) {
		t.Errorf("bad id: %v", err)
	}
	// A closed store: every read and write fails, and the manager still starts.
	writeTrainers(t, "trainers:\n  - {name: 'bad name', command: x}\n")
	closeStorage(t, f.s)
	if err := f.m.saveJob(jobs[0]); err == nil {
		t.Error("save on a closed store")
	}
	if err := f.m.recoverJobs(); err == nil {
		t.Error("recover on a closed store")
	}
	if m := newTrainingManager(f.s); len(m.profiles.errors) != 1 {
		t.Errorf("profile errors: %v", m.profiles.errors)
	}
	f.m.update(jobs[0], func() {}) // logs, does not panic
	rec := httptest.NewRecorder()
	listTrainingJobs(rec, f.m, trainingKindRun)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("list on a closed store: %d", rec.Code)
	}
	ds := pairsRequest()
	ds.AcknowledgeUnlicensed = true
	if _, err := f.m.writeDataset("ds_0000000000000000", &preparedDataset{req: ds, budget: 1 << 20}); err == nil {
		t.Error("a dataset was written from a closed store")
	}
	if _, err := f.m.submitDataset(datasetRequest{Collections: []string{"x"}, Format: datasetText, AcknowledgeUnlicensed: true}, ""); err == nil {
		t.Error("a dataset was submitted to a closed store")
	}
	if _, err := f.m.submitRun(runRequest{Profile: "noeval", Dataset: &ds}, ""); err == nil || f.m.runningOf("noeval") != 0 {
		t.Errorf("a run on a closed store: %v, slots %d", err, f.m.runningOf("noeval"))
	}
}

func TestTrainingDir(t *testing.T) {
	t.Setenv(trainingDirEnv, "")
	if got := (&Server{Path: filepath.Join("data", "mddb.db")}).trainingDir(); got != filepath.Join("data", "training") {
		t.Errorf("beside the database: %s", got)
	}
	if got := (&Server{}).trainingDir(); got != filepath.Join(os.TempDir(), "mddb-training") {
		t.Errorf("no database path: %s", got)
	}
}

func TestTrainingFilesErrors(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	good := f.m.root
	f.m.root = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f.m.root, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.jobRoot("ds_0000000000000000", true); err == nil {
		t.Error("jobRoot under a file")
	}
	if err := f.m.removeJobDir("ds_0000000000000000"); err == nil {
		t.Error("removeJobDir under a file")
	}
	if _, err := f.m.diskBudget(); err == nil {
		t.Error("diskBudget under a file")
	}
	if _, err := f.m.writeDataset("ds_0000000000000000", &preparedDataset{req: pairsRequest(), budget: 1 << 30}); err == nil {
		t.Error("writeDataset under a file")
	}
	f.m.root = good
	if _, err := f.m.jobRoot("x", true); !errors.Is(err, errTrainingNotFound) {
		t.Errorf("bad id: %v", err)
	}
	saved, reserve := trainingDiskSpace, trainingDiskReserve
	defer func() { trainingDiskSpace, trainingDiskReserve = saved, reserve }()
	trainingDiskSpace = func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs failed") }
	if _, err := f.m.diskBudget(); err == nil {
		t.Error("a disk space error was ignored")
	}
	trainingDiskSpace, trainingDiskReserve = saved, ^uint64(0)
	if _, err := f.m.submitDataset(pairsRequest(), ""); !errors.Is(err, errTrainingDisk) {
		t.Errorf("full disk: %v", err)
	}
	trainingDiskReserve = reserve

	jr, err := f.m.jobRoot("ds_0000000000000000", true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = jr.Close() }()
	if _, err := writeRecords(jr, "none/x.jsonl", nil, 10); err == nil {
		t.Error("writeRecords into a missing directory")
	}
	if _, err := writeRecords(jr, "x.jsonl", []datasetRecord{{Text: "long enough"}}, 5); !errors.Is(err, errTrainingDisk) {
		t.Errorf("budget: %v", err)
	}
	if _, err := f.m.writeDataset("ds_0000000000000000", &preparedDataset{req: pairsRequest(), budget: 5}); !errors.Is(err, errTrainingDisk) {
		t.Errorf("writeDataset past its budget: %v", err)
	}
	if _, err := cutRecords(jr, jr, "missing", "y", 1); err == nil {
		t.Error("cut from a missing file")
	}
	if _, err := cutRecords(jr, jr, "x.jsonl", "none/y", 1); err == nil {
		t.Error("cut into a missing directory")
	}
	if _, _, err := openRegular(jr, "."); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a directory was opened: %v", err)
	}
	if _, err := findResult(jr, "*"); err == nil {
		t.Error("a result without an output directory")
	}
	if _, _, err := f.m.openResult(&trainingJob{ID: "run_0000000000000000"}); !errors.Is(err, errTrainingNoResult) {
		t.Errorf("no result: %v", err)
	}
	j := &trainingJob{ID: "run_0000000000000000", OutputPattern: "*.gguf", Result: &trainingResult{File: "../dataset/train.jsonl"}}
	if _, _, err := f.m.openResult(j); !errors.Is(err, errTrainingNoResult) {
		t.Errorf("a name outside the pattern: %v", err)
	}
	if f.m.logTail("run_0000000000000000") != "" {
		t.Error("a log tail without a log")
	}
	// A log longer than the tail is read from its end.
	if err := os.WriteFile(f.m.jobPath("ds_0000000000000000", trainingLogName), []byte(strings.Repeat("a", trainingLogTail)+"END"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tail := f.m.logTail("ds_0000000000000000"); len(tail) != trainingLogTail || !strings.HasSuffix(tail, "END") {
		t.Errorf("tail: %d", len(tail))
	}
	// A file swapped between the check and the open is refused.
	saved2 := openInRoot
	defer func() { openInRoot = saved2 }()
	openInRoot = func(r *os.Root, _ string) (*os.File, error) { return r.Open(".") }
	if _, _, err := f.m.openJobFile("ds_0000000000000000", trainingLogName); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Errorf("swapped file: %v", err)
	}
	openInRoot = func(*os.Root, string) (*os.File, error) { return nil, os.ErrPermission }
	if _, _, err := f.m.openJobFile("ds_0000000000000000", trainingLogName); !errors.Is(err, os.ErrPermission) {
		t.Errorf("open failure: %v", err)
	}
	openInRoot = saved2
	closed, _ := os.Open(f.m.jobPath("ds_0000000000000000", trainingLogName))
	_ = closed.Close()
	if _, err := readEvalReport(closed); err == nil {
		t.Error("a report read from a closed file")
	}
}

func TestTrainingExecPieces(t *testing.T) {
	logFile, err := os.Create(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	log := &trainingLog{f: logFile}
	run := &commandRun{spec: &commandSpec{Command: filepath.Join(t.TempDir(), "missing")}, timeout: time.Second, log: log}
	if code, err := run.run(context.Background()); code != -1 || err == nil {
		t.Errorf("a missing command: %d %v", code, err)
	}
	var got []string
	w := &progressWriter{log: log, prefix: "P", onProgress: func(p float64, m string) { got = append(got, m) }}
	_, _ = w.Write([]byte("other\nP 101\nP x\nP 5% five\r\n" + strings.Repeat("y", maxProgressLine+1)))
	if len(w.line) != 0 {
		t.Error("an endless line was kept")
	}
	_, _ = w.Write([]byte("P 7 seven"))
	w.flush()
	if strings.Join(got, ",") != "five,seven" {
		t.Errorf("progress: %q", got)
	}
	quiet := &progressWriter{log: log}
	if n, _ := quiet.Write([]byte("P 1\n")); n != 4 {
		t.Error("write without a prefix")
	}
	log.close()
	log.close()
	if n, _ := log.Write([]byte("after")); n != 5 {
		t.Error("a write to a closed log failed")
	}
}

func TestTrainingManagerEdges(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	if _, err := f.m.cancel("run_0000000000000000", trainingKindRun); !errors.Is(err, errTrainingNotFound) {
		t.Errorf("cancel unknown: %v", err)
	}
	if err := f.m.remove("run_0000000000000000", trainingKindRun); !errors.Is(err, errTrainingNotFound) {
		t.Errorf("remove unknown: %v", err)
	}
	ds, err := f.m.submitDataset(pairsRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, ds.ID)
	if _, err := f.m.jobOfKind(ds.ID, trainingKindRun); !errors.Is(err, errTrainingNotFound) {
		t.Errorf("a dataset read as a run: %v", err)
	}
	// A dataset a running job uses, and the running job itself, stay.
	run, err := f.m.submitRun(runRequest{Profile: "stubborn", DatasetID: ds.ID}, "")
	if err != nil {
		t.Fatal(err)
	}
	waitLong(t, func() bool { j, _ := f.m.getJob(run.ID); return j.Stage == stageTraining })
	if err := f.m.remove(ds.ID, trainingKindDataset); !errors.Is(err, errTrainingState) {
		t.Errorf("remove a dataset in use: %v", err)
	}
	if err := f.m.remove(run.ID, trainingKindRun); !errors.Is(err, errTrainingState) {
		t.Errorf("remove a running job: %v", err)
	}
	if _, err := f.m.submitEvaluate(run.ID); !errors.Is(err, errTrainingState) {
		t.Errorf("evaluate a running job: %v", err)
	}
	// Shutdown gives up waiting after trainingStopWait.
	wait := trainingStopWait
	trainingStopWait = time.Millisecond
	f.m.stop()
	trainingStopWait = wait
	f.wait(t, run.ID)

	g := newTrainingFixture(t, "")
	g.seedRecipes(t)
	if _, err := g.m.submitEvaluate("run_0000000000000000"); !errors.Is(err, errTrainingNotFound) {
		t.Errorf("evaluate unknown: %v", err)
	}
	pairs := pairsRequest()
	noeval := g.startRun(t, runRequest{Profile: "noeval", Dataset: &pairs})
	if _, err := g.m.submitEvaluate(noeval.ID); !errors.Is(err, errTrainingInvalid) {
		t.Errorf("evaluate without an evaluate command: %v", err)
	}
	full := g.startRun(t, runRequest{Profile: "fake", Dataset: &pairs})
	g.m.slots["fake"] = 1
	if _, err := g.m.submitEvaluate(full.ID); !errors.Is(err, errTrainingBusy) {
		t.Errorf("evaluate past maxConcurrent: %v", err)
	}
	g.m.slots["fake"] = 0
	g.m.update(full, func() { full.EvalParameters = map[string]any{"limit": 0.0} })
	if _, err := g.m.submitEvaluate(full.ID); !errors.Is(err, errTrainingInvalid) {
		t.Errorf("stored values the profile now refuses: %v", err)
	}
	g.m.profiles.list = g.m.profiles.list[1:]
	if _, err := g.m.submitEvaluate(full.ID); !errors.Is(err, errTrainingInvalid) {
		t.Errorf("a profile that is gone: %v", err)
	}
	// A trial whose dataset lost its files fails at the cut.
	ds2, _ := g.m.submitDataset(pairs, "")
	g.wait(t, ds2.ID)
	if err := os.Remove(g.m.jobPath(ds2.ID, datasetSubdir, trainFile)); err != nil {
		t.Fatal(err)
	}
	if j := g.startRun(t, runRequest{Profile: "noeval", DatasetID: ds2.ID, Trial: true}); !strings.Contains(j.Error, "trial:") {
		t.Errorf("missing train file: %+v", j)
	}
	if err := os.RemoveAll(g.m.jobPath(ds2.ID)); err != nil {
		t.Fatal(err)
	}
	if j := g.startRun(t, runRequest{Profile: "noeval", DatasetID: ds2.ID, Trial: true}); !strings.Contains(j.Error, "trial:") {
		t.Errorf("missing dataset directory: %+v", j)
	}
}

func TestTrainingHandlerEdges(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	h := f.trainingHTTP(t, true, false)
	am, cleanup := authMwSetup(t)
	defer cleanup()
	f.s.AuthManager = am
	reader := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "reader"})
	rec := httptest.NewRecorder()
	f.s.training(func(http.ResponseWriter, *http.Request, *trainingManager) { t.Error("reached") })(rec,
		httptest.NewRequest(http.MethodGet, "/v1/training/jobs", nil).WithContext(reader))
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d", rec.Code)
	}
	if trainingUser(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(reader)) != "reader" {
		t.Error("user from the claims")
	}
	f.s.AuthManager = nil
	if rec, _ := trainingDo(t, h, http.MethodDelete, "/v1/training/jobs/run_0000000000000000", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete unknown: %d", rec.Code)
	}
	empty := `{"collections":["recipes"],"format":"text","lang":"de"}`
	_, out := trainingDo(t, h, http.MethodPost, "/v1/training/datasets", empty, "")
	ds := f.wait(t, out["jobId"].(string))
	if rec, _ := trainingDo(t, h, http.MethodGet, "/v1/training/datasets/"+ds.ID+"/download", "", ""); rec.Code != http.StatusConflict {
		t.Errorf("download of a failed dataset: %d", rec.Code)
	}
	_, out = trainingDo(t, h, http.MethodPost, "/v1/training/jobs", `{"profile":"fail","dataset":{"collections":["recipes"],"format":"text"}}`, "")
	run := f.wait(t, out["jobId"].(string))
	if rec, _ := trainingDo(t, h, http.MethodGet, "/v1/training/jobs/"+run.ID+"/download", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("download of a failed run: %d", rec.Code)
	}
}
