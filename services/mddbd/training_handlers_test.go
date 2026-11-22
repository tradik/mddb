package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"mddb/internal/audit"
	json "mddb/internal/jsonx"
)

// trainingHTTP is the fixture's server behind its real routes, gates and
// auth, with the module switched as on says.
func (f *trainingFixture) trainingHTTP(t *testing.T, on, auth bool) http.Handler {
	t.Helper()
	f.s.Features = NewFeatureManager(f.s.DB)
	if err := f.s.Features.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Features.Set(FeatureModelTraining, on, "test"); err != nil {
		t.Fatal(err)
	}
	return esHandler(t, f.s, auth)
}

// trainingDo sends one request; key is a bearer credential or "".
func trainingDo(t *testing.T, h http.Handler, method, path, body, key string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return openAIDo(t, h, method, path, body, key)
}

func TestTrainingModuleOffAnswers404(t *testing.T) {
	t.Setenv(featuresForceOffEnv, "")
	f := newTrainingFixture(t, "")
	h := f.trainingHTTP(t, false, false)
	for _, p := range []string{"/v1/training/profiles", "/v1/training/datasets", "/v1/training/datasets/ds_0000000000000000",
		"/v1/training/jobs", "/v1/training/jobs/run_0000000000000000/download"} {
		if rec, out := trainingDo(t, h, http.MethodGet, p, "", ""); rec.Code != http.StatusNotFound || out["feature"] != FeatureModelTraining {
			t.Errorf("%s while off: %d %v", p, rec.Code, out)
		}
	}
	// MDDB_FEATURES_FORCE_OFF keeps it off whatever the switch says.
	t.Setenv(featuresForceOffEnv, FeatureModelTraining)
	f.s.Features = NewFeatureManager(f.s.DB)
	if err := f.s.Features.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Features.Set(FeatureModelTraining, true, "test"); err == nil {
		t.Error("a forced-off module was switched on")
	}
	if rec, _ := trainingDo(t, h, http.MethodGet, "/v1/training/profiles", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("forced off: %d", rec.Code)
	}
}

func TestTrainingHTTPFlow(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	f.s.AuditManager = audit.NewAuditManager(f.s.DB, true, 1)
	if err := f.s.AuditManager.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	f.s.AuditManager.Start()
	h := f.trainingHTTP(t, true, false)

	rec, out := trainingDo(t, h, http.MethodGet, "/v1/training/profiles", "", "")
	exe, _ := os.Executable()
	if rec.Code != http.StatusOK || out["configured"] != true || strings.Contains(rec.Body.String(), "command") ||
		strings.Contains(rec.Body.String(), exe) || strings.Contains(rec.Body.String(), "{value}") {
		t.Fatalf("profiles reveal the command line: %d %s", rec.Code, rec.Body.String())
	}

	// A dataset over an unlicensed collection needs the acknowledgement.
	body := `{"collections":["recipes","notes"],"format":"text"}`
	if rec, out = trainingDo(t, h, http.MethodPost, "/v1/training/datasets", body, ""); rec.Code != http.StatusBadRequest ||
		!strings.Contains(out["error"].(string), "notes") {
		t.Fatalf("unlicensed: %d %v", rec.Code, out)
	}
	body = `{"collections":["recipes","notes"],"format":"text","acknowledgeUnlicensed":true,"validationRatio":0.25}`
	if rec, out = trainingDo(t, h, http.MethodPost, "/v1/training/datasets", body, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("dataset: %d %v", rec.Code, out)
	}
	ds := f.wait(t, out["jobId"].(string))
	if ds.Summary.UnlicensedAcknowledged[0] != "notes" || ds.Summary.Collections[0].License != "CC-BY-4.0" {
		t.Errorf("licence summary: %+v", ds.Summary)
	}
	if rec, out = trainingDo(t, h, http.MethodGet, "/v1/training/datasets/"+ds.ID, "", ""); rec.Code != http.StatusOK || out["job"] == nil {
		t.Errorf("dataset status: %d %v", rec.Code, out)
	}
	rec, _ = trainingDo(t, h, http.MethodGet, "/v1/training/datasets/"+ds.ID+"/download?part=train", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"license":"CC-BY-4.0"`) ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), ds.ID+"-train.jsonl") {
		t.Errorf("download: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if rec, _ = trainingDo(t, h, http.MethodGet, "/v1/training/datasets/"+ds.ID+"/download?part=../../x", "", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad part: %d", rec.Code)
	}
	if rec, out = trainingDo(t, h, http.MethodGet, "/v1/training/datasets", "", ""); rec.Code != http.StatusOK || out["total"] != 1.0 {
		t.Errorf("dataset list: %d %v", rec.Code, out)
	}

	// A run, its status, its download and its end.
	body = `{"name":"r","profile":"fake","datasetId":"` + ds.ID + `","parameters":{"epochs":2}}`
	if rec, out = trainingDo(t, h, http.MethodPost, "/v1/training/jobs", body, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %v", rec.Code, out)
	}
	id := out["jobId"].(string)
	run := f.wait(t, id)
	if rec, out = trainingDo(t, h, http.MethodGet, "/v1/training/jobs/"+id, "", ""); rec.Code != http.StatusOK || !strings.Contains(out["log"].(string), "training") {
		t.Errorf("run status: %d %v", rec.Code, out)
	}
	rec, _ = trainingDo(t, h, http.MethodGet, "/v1/training/jobs/"+id+"/download", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Disposition"), "model.gguf") {
		t.Errorf("result download: %d %v", rec.Code, rec.Header())
	}
	if rec, _ = trainingDo(t, h, http.MethodPost, "/v1/training/jobs/"+id+"/cancel", "", ""); rec.Code != http.StatusConflict {
		t.Errorf("cancel of a finished run: %d", rec.Code)
	}
	if rec, _ = trainingDo(t, h, http.MethodPost, "/v1/training/jobs/"+id+"/evaluate", "", ""); rec.Code != http.StatusAccepted {
		t.Errorf("evaluate: %d", rec.Code)
	}
	f.wait(t, id)
	if rec, out = trainingDo(t, h, http.MethodGet, "/v1/training/jobs", "", ""); rec.Code != http.StatusOK || out["total"] != 1.0 {
		t.Errorf("run list: %d %v", rec.Code, out)
	}
	if rec, _ = trainingDo(t, h, http.MethodDelete, "/v1/training/jobs/"+id, "", ""); rec.Code != http.StatusOK {
		t.Errorf("delete run: %d", rec.Code)
	}
	if _, err := os.Stat(f.m.jobPath(run.ID)); !os.IsNotExist(err) {
		t.Errorf("the run's directory stays: %v", err)
	}
	if rec, _ = trainingDo(t, h, http.MethodDelete, "/v1/training/datasets/"+ds.ID, "", ""); rec.Code != http.StatusOK {
		t.Errorf("delete dataset: %d", rec.Code)
	}
	for _, p := range []string{"/v1/training/jobs/" + id, "/v1/training/datasets/" + ds.ID, "/v1/training/jobs/" + ds.ID, "/v1/training/datasets/..%2E", "/v1/training/jobs/run_ZZ"} {
		if rec, _ = trainingDo(t, h, http.MethodGet, p, "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	f.s.AuditManager.Stop()
	events, err := f.s.AuditManager.Query(audit.QueryFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, ev := range events {
		seen[ev.Action+":"+ev.Result] = true
	}
	for _, want := range []string{"training.dataset:fail", "training.dataset:ok", "training.start:ok", "training.download:ok",
		"training.cancel:fail", "training.evaluate:ok", "training.delete:ok"} {
		if !seen[want] {
			t.Errorf("audit lacks %s: %v", want, seen)
		}
	}
}

func TestTrainingHTTPRefusals(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	h := f.trainingHTTP(t, true, false)
	ds := `"dataset":{"collections":["recipes"],"format":"text"}`
	for name, body := range map[string]string{
		"unknown profile":   `{"profile":"nope",` + ds + `}`,
		"unknown parameter": `{"profile":"fake",` + ds + `,"parameters":{"command":"/bin/sh"}}`,
		"shell in a string": `{"profile":"fake",` + ds + `,"parameters":{"tag":"$(id)"}}`,
		"semicolon":         `{"profile":"fake",` + ds + `,"parameters":{"tag":"a;rm"}}`,
		"leading dash":      `{"profile":"fake",` + ds + `,"parameters":{"tag":"--output=/etc/passwd"}}`,
		"newline":           `{"profile":"fake",` + ds + `,"parameters":{"tag":"a\nb"}}`,
		"nul":               `{"profile":"fake",` + ds + `,"parameters":{"tag":"a\u0000b"}}`,
		"path":              `{"profile":"fake",` + ds + `,"parameters":{"tag":"../../x"}}`,
		"too long":          `{"profile":"fake",` + ds + `,"parameters":{"tag":"` + strings.Repeat("a", 17) + `"}}`,
		"over max":          `{"profile":"fake",` + ds + `,"parameters":{"epochs":11}}`,
		"not whole":         `{"profile":"fake",` + ds + `,"parameters":{"epochs":1.5}}`,
		"enum":              `{"profile":"fake",` + ds + `,"parameters":{"method":"c"}}`,
		"bool type":         `{"profile":"fake",` + ds + `,"parameters":{"verbose":"yes"}}`,
		"eval parameter":    `{"profile":"fake",` + ds + `,"evaluateParameters":{"limit":0}}`,
		"eval on noeval":    `{"profile":"noeval",` + ds + `,"evaluateParameters":{"limit":1}}`,
		"no dataset":        `{"profile":"fake"}`,
		"both datasets":     `{"profile":"fake","datasetId":"ds_0000000000000000",` + ds + `}`,
		"missing dataset":   `{"profile":"fake","datasetId":"ds_0000000000000000"}`,
		"bad dataset":       `{"profile":"fake","dataset":{"collections":[],"format":"text"}}`,
		"broken json":       `{"profile":`,
	} {
		if rec, out := trainingDo(t, h, http.MethodPost, "/v1/training/jobs", body, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", name, rec.Code, out)
		}
	}
	if rec, _ := trainingDo(t, h, http.MethodPost, "/v1/training/datasets", `{`, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("broken dataset json: %d", rec.Code)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPut, "/v1/training/profiles"}, {http.MethodPut, "/v1/training/datasets"}, {http.MethodPut, "/v1/training/jobs"},
		{http.MethodPost, "/v1/training/jobs/run_0000000000000000/command"}, {http.MethodPost, "/v1/training/datasets/ds_0000000000000000"},
	} {
		if rec, _ := trainingDo(t, h, c.method, c.path, "", ""); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
	// No trainer configured: datasets still build, a run is a clear 409.
	t.Setenv(trainersConfigEnv, "")
	f.s.Training = newTrainingManager(f.s)
	rec, out := trainingDo(t, h, http.MethodPost, "/v1/training/jobs", `{"profile":"fake",`+ds+`}`, "")
	if rec.Code != http.StatusConflict || !strings.Contains(out["error"].(string), "no trainer is configured") {
		t.Errorf("no trainer: %d %v", rec.Code, out)
	}
	if rec, out = trainingDo(t, h, http.MethodGet, "/v1/training/profiles", "", ""); out["configured"] != false {
		t.Errorf("profiles without a file: %d %v", rec.Code, out)
	}
	if rec, _ = trainingDo(t, h, http.MethodPost, "/v1/training/datasets", `{"collections":["recipes"],"format":"text"}`, ""); rec.Code != http.StatusAccepted {
		t.Errorf("dataset without a trainer: %d", rec.Code)
	}
	f.s.Training.stop()
	f.s.Training = nil
	if rec, _ = trainingDo(t, h, http.MethodGet, "/v1/training/profiles", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no manager: %d", rec.Code)
	}
}

func TestTrainingNeedsAdminAndRefusesScopedKeys(t *testing.T) {
	f := newTrainingFixture(t, "")
	am, cleanup := authMwSetup(t)
	defer cleanup()
	f.s.AuthManager = am
	h := f.trainingHTTP(t, true, true)
	if _, err := am.CreateUser("reader", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := am.SetPermission(&Permission{Username: "reader", Collection: "*", Read: true, Write: true}); err != nil {
		t.Fatal(err)
	}
	reader, err := am.CreateAPIKey("reader", "k", 0)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := am.CreateAPIKey("admin", "k", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/v1/training/profiles", "/v1/training/jobs", "/v1/training/datasets"} {
		if rec, _ := trainingDo(t, h, http.MethodGet, p, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without credentials: %d", p, rec.Code)
		}
		if rec, _ := trainingDo(t, h, http.MethodGet, p, "", reader); rec.Code != http.StatusForbidden {
			t.Errorf("%s as a non-admin with write on *: %d", p, rec.Code)
		}
		if rec, _ := trainingDo(t, h, http.MethodGet, p, "", admin); rec.Code != http.StatusOK {
			t.Errorf("%s as admin: %d", p, rec.Code)
		}
	}
	// A scoped MCP key is refused before anything else, even with auth off.
	f.s.AuthManager = nil
	called := false
	handler := f.s.training(func(http.ResponseWriter, *http.Request, *trainingManager) { called = true })
	ctx := withKeyScope(context.Background(), mcpKeyScope{collections: []string{"recipes"}})
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/v1/training/jobs", nil).WithContext(ctx))
	if rec.Code != http.StatusForbidden || called {
		t.Errorf("scoped key: %d %v", rec.Code, called)
	}
	// And no MCP tool reaches the module.
	for name := range mcpToolGroup {
		if strings.Contains(name, "train") {
			t.Errorf("MCP tool %s reaches training", name)
		}
	}
}

func TestTrainingErrorStatus(t *testing.T) {
	for err, want := range map[error]int{
		errTrainingDisk: http.StatusInsufficientStorage, errTrainingBusy: http.StatusConflict,
		errTrainingNoResult: http.StatusNotFound, os.ErrPermission: http.StatusInternalServerError,
	} {
		rec := httptest.NewRecorder()
		trainingError(rec, err)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != want || out["error"] == "" {
			t.Errorf("%v: %d", err, rec.Code)
		}
	}
	if jobID(nil) != "" || trainingUser(httptest.NewRequest(http.MethodGet, "/", nil)) != "" {
		t.Error("empty id or user")
	}
}
