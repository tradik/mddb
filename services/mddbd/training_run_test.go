package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// startRun submits a run and waits for its end.
func (f *trainingFixture) startRun(t *testing.T, req runRequest) *trainingJob {
	t.Helper()
	j, err := f.m.submitRun(req, "admin")
	if err != nil {
		t.Fatalf("submitRun: %v", err)
	}
	return f.wait(t, j.ID)
}

// resultText is a run's result file.
func (f *trainingFixture) resultText(t *testing.T, j *trainingJob) string {
	t.Helper()
	file, _, err := f.m.openResult(j)
	if err != nil {
		t.Fatalf("openResult: %v", err)
	}
	defer func() { _ = file.Close() }()
	b, _ := io.ReadAll(file)
	return string(b)
}

func TestTrainingRunEndToEnd(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	t.Setenv("MDDB_JWT_SECRET", "a-server-secret")
	t.Setenv("SOME_TOKEN", "not on the allowlist")
	ds := pairsRequest()
	j := f.startRun(t, runRequest{Name: "full", Profile: "fake", Dataset: &ds,
		Parameters: map[string]any{"epochs": 4.0, "rate": 0.5, "verbose": true, "tag": "v1"}})
	if j.Status != BulkJobCompleted || j.Result == nil || j.Result.File != "model.gguf" || *j.ExitCode != 0 {
		t.Fatalf("run: %+v", j)
	}
	got := f.resultText(t, j)
	for _, want := range []string{"--epochs\x1f4", "--rate=0.5", "--method\x1fa", "--verbose", "--tag\x1fv1", "--data=" + f.m.jobPath(j.DatasetID, "dataset", "train.jsonl"), "trial=0", "lines=2"} {
		if !strings.Contains(got, want) {
			t.Errorf("result lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "MDDB_JWT_SECRET") || strings.Contains(got, "SOME_TOKEN") || !strings.Contains(got, "MDDB_TRAIN_DATASET") {
		t.Errorf("environment: %s", got)
	}
	ev := j.Evaluation
	if ev == nil || ev.Base == nil || ev.Result == nil || ev.Base.Report.Score != 0.4 || ev.Result.Report.Score != 0.7 || ev.Delta == nil || len(ev.Notes) != 0 {
		t.Fatalf("evaluation: %+v", ev)
	}
	if ev.Result.Report.Samples[0].Input != "<b>i</b>" || j.Parameters["epochs"] != "4" || j.Format != datasetPairs || j.ValidationRecords != 2 {
		t.Errorf("job: %+v", j)
	}
	if j.Progress == nil || *j.Progress != 100 {
		t.Errorf("progress: %v", j.Progress)
	}
	log := f.m.logTail(j.ID)
	for _, want := range []string{"=== dataset", "=== evaluate-base", "=== training", "=== evaluate-result"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	// A finished run is evaluated again on request.
	again, err := f.m.submitEvaluate(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j = f.wait(t, again.ID); j.Status != BulkJobCompleted || j.Evaluation.Result.Report == nil {
		t.Fatalf("re-evaluation: %+v", j)
	}
}

func TestTrainingTrialRun(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	ds, err := f.m.submitDataset(datasetRequest{Collections: []string{"recipes", "techniques"}, Format: datasetText, ValidationRatio: 0.2}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if d := f.wait(t, ds.ID); d.Status != BulkJobCompleted || d.Summary.TrainRecords != 4 {
		t.Fatalf("dataset: %+v", d)
	}
	// trialParameters win over the request's values; the cut is trialRecords.
	j := f.startRun(t, runRequest{Profile: "fake", DatasetID: ds.ID, Trial: true, Parameters: map[string]any{"epochs": 9.0}})
	got := f.resultText(t, j)
	if j.Status != BulkJobCompleted || !j.Trial || j.Parameters["epochs"] != "1" || !strings.Contains(got, "--epochs\x1f1") ||
		!strings.Contains(got, "trial=1") || !strings.Contains(got, "lines=2") {
		t.Fatalf("trial: %+v\n%s", j, got)
	}
	if j.Evaluation == nil || !strings.Contains(j.Evaluation.Skipped, "pairs") {
		t.Errorf("a text dataset is not evaluated: %+v", j.Evaluation)
	}
	// Re-evaluating it reads the trial's cut and is skipped the same way.
	if _, err := f.m.submitEvaluate(j.ID); err != nil {
		t.Fatal(err)
	}
	if j = f.wait(t, j.ID); j.Evaluation == nil || j.Evaluation.Skipped == "" {
		t.Errorf("re-evaluation of a text run: %+v", j.Evaluation)
	}
	// The request's own limit caps the cut.
	small := pairsRequest()
	small.MaxRecords = 1
	small.ValidationRatio = 0
	j = f.startRun(t, runRequest{Profile: "fake", Dataset: &small, Trial: true})
	if got := f.resultText(t, j); !strings.Contains(got, "lines=1") || j.Evaluation.Skipped == "" {
		t.Errorf("trial capped by maxRecords: %s %+v", got, j.Evaluation)
	}
}

func TestTrainingEvaluationOutcomes(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	ds := pairsRequest()
	cases := map[string]func(j *trainingJob) bool{
		"worse": func(j *trainingJob) bool {
			return j.Status == BulkJobCompleted && *j.Evaluation.Delta < 0 && len(j.Evaluation.Notes) == 1
		},
		"nobase": func(j *trainingJob) bool {
			return j.Evaluation.Base == nil && strings.Contains(j.Evaluation.Notes[0], "No baseline")
		},
		"noeval": func(j *trainingJob) bool { return strings.Contains(j.Evaluation.Skipped, "no evaluate") },
		"evalbad": func(j *trainingJob) bool {
			return j.Status == trainingDoneWithWarnings && strings.Contains(j.Evaluation.Result.Error, "JSON")
		},
		"evalbig": func(j *trainingJob) bool { return strings.Contains(j.Evaluation.Result.Error, "larger") },
		"evalexit": func(j *trainingJob) bool {
			return *j.Evaluation.Result.ExitCode == 2 && j.Status == trainingDoneWithWarnings
		},
		"evalslow": func(j *trainingJob) bool { return strings.Contains(j.Evaluation.Result.Error, "timed out") },
	}
	for profile, check := range cases {
		j := f.startRun(t, runRequest{Profile: profile, Dataset: &ds})
		if j.Result == nil || !check(j) {
			t.Errorf("%s: %+v %+v", profile, j, j.Evaluation)
		}
	}
	off := false
	if j := f.startRun(t, runRequest{Profile: "fake", Dataset: &ds, Evaluate: &off}); j.Evaluation != nil {
		t.Errorf("evaluate: false: %+v", j.Evaluation)
	}
	noSplit := pairsRequest()
	noSplit.ValidationRatio = 0
	if j := f.startRun(t, runRequest{Profile: "fake", Dataset: &noSplit}); !strings.Contains(j.Evaluation.Skipped, "validation") {
		t.Errorf("no split: %+v", j.Evaluation)
	}
}

func TestTrainingReportOutsideTheJobIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs a privilege on Windows")
	}
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	ds := pairsRequest()
	j := f.startRun(t, runRequest{Profile: "evalescape", Dataset: &ds})
	if j.Status != trainingDoneWithWarnings || !strings.Contains(j.Evaluation.Result.Error, "not a regular file") {
		t.Fatalf("escape: %+v %+v", j, j.Evaluation.Result)
	}
}

func TestTrainingRunFailures(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	ds := pairsRequest()
	if j := f.startRun(t, runRequest{Profile: "fail", Dataset: &ds}); j.Status != BulkJobFailed || *j.ExitCode != 3 ||
		!strings.Contains(j.Error, "code 3") || !strings.Contains(f.m.logTail(j.ID), "boom") {
		t.Errorf("fail: %+v", j)
	}
	if j := f.startRun(t, runRequest{Profile: "noout", Dataset: &ds}); j.Status != BulkJobFailed || !strings.Contains(j.Error, "no regular file") {
		t.Errorf("noout: %+v", j)
	}
	if j := f.startRun(t, runRequest{Profile: "slow", Dataset: &ds}); j.Status != BulkJobFailed || !strings.Contains(j.Error, "timed out") {
		t.Errorf("slow: %+v", j)
	}
	empty := datasetRequest{Collections: []string{"recipes"}, Format: datasetText, Lang: "de"}
	if j := f.startRun(t, runRequest{Profile: "fake", Dataset: &empty}); j.Status != BulkJobFailed || !strings.Contains(j.Error, "dataset:") {
		t.Errorf("empty dataset: %+v", j)
	}
	saved := trainingLogCap
	trainingLogCap = 1000
	defer func() { trainingLogCap = saved }()
	if j := f.startRun(t, runRequest{Profile: "noisy", Dataset: &ds}); !strings.Contains(f.m.logTail(j.ID), "log truncated") {
		t.Errorf("log cap: %q", f.m.logTail(j.ID))
	}
}

func TestTrainingSymlinkedResultIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs a privilege on Windows")
	}
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	ds := pairsRequest()
	if j := f.startRun(t, runRequest{Profile: "symlink", Dataset: &ds}); j.Status != BulkJobFailed || j.Result != nil {
		t.Errorf("symlinked result: %+v", j)
	}
	// A result replaced by a link after the run is not served either.
	j := f.startRun(t, runRequest{Profile: "noeval", Dataset: &ds})
	out := f.m.jobPath(j.ID, "output", "model.gguf")
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.m.root, "..", "x"), out); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.openResult(j); err == nil {
		t.Error("a link was served")
	}
	// So is an output directory replaced by a link out of the training root.
	dir := f.m.jobPath(j.ID, "output")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.openResult(j); err == nil {
		t.Error("an output directory out of the root was followed")
	}
}

func TestTrainingCancelAndShutdown(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	ds := pairsRequest()
	j, err := f.m.submitRun(runRequest{Profile: "sleep", Dataset: &ds}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	waitLong(t, func() bool { cur, _ := f.m.getJob(j.ID); return cur.Stage == stageTraining && cur.Progress != nil })
	if _, err := f.m.submitRun(runRequest{Profile: "sleep", Dataset: &ds}, "admin"); err == nil {
		t.Error("a second run past maxConcurrent was accepted")
	}
	if _, err := f.m.cancel(j.ID, trainingKindRun); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if j = f.wait(t, j.ID); j.Status != BulkJobCancelled || time.Since(start) > 20*time.Second {
		t.Fatalf("cancel: %+v", j)
	}
	if _, err := f.m.cancel(j.ID, trainingKindRun); err == nil {
		t.Error("a finished job was cancelled")
	}
	// A shutdown interrupts the running evaluation.
	j, err = f.m.submitRun(runRequest{Profile: "evalsleep", Dataset: &ds}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	waitLong(t, func() bool { cur, _ := f.m.getJob(j.ID); return cur.Stage == stageEvaluateBase })
	f.m.stop()
	if j, _ = f.m.getJob(j.ID); j.Status != trainingInterrupted {
		t.Fatalf("shutdown: %+v", j)
	}
}

// waitLong polls cond for up to 30 seconds: a process has to start.
func waitLong(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not reached")
}
