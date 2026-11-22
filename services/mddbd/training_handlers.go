package main

import (
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"mddb/internal/audit"
	json "mddb/internal/jsonx"
)

// HTTP endpoints of the model training module, all under /v1/training and
// all admin: a run starts a process on the server. A call made with a scoped
// MCP key is refused whatever the key's reach, and no MCP tool, gRPC method
// or GraphQL field reaches the module. Starting, cancelling, deleting,
// evaluating and downloading are written to the audit log with the user.

const (
	trainingJobsPrefix     = "/v1/training/jobs/"
	trainingDatasetsPrefix = "/v1/training/datasets/"
)

// trainingHandler is a training endpoint, given the manager.
type trainingHandler func(w http.ResponseWriter, r *http.Request, m *trainingManager)

// training admits a request to a training endpoint: an admin, not through a
// scoped MCP key, on a server whose manager runs.
func (s *Server) training(h trainingHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, scoped := keyScopeFromContext(r.Context()); scoped {
			http.Error(w, `{"error":"model training is not reachable with a scoped MCP key"}`, http.StatusForbidden)
			return
		}
		if !s.requireAdmin(w, r) {
			return
		}
		if s.Training == nil {
			http.Error(w, `{"error":"model training is not running on this server"}`, http.StatusServiceUnavailable)
			return
		}
		h(w, r, s.Training)
	}
}

// trainingUser is the caller's name for the job record and the audit log.
func trainingUser(r *http.Request) string {
	if c, ok := GetClaimsFromContext(r.Context()); ok && c != nil {
		return c.Username
	}
	return ""
}

// auditTraining records an action on a training job.
func (s *Server) auditTraining(r *http.Request, action, id string, err error) {
	if s.AuditManager == nil {
		return
	}
	result, detail := "ok", id
	if err != nil {
		result, detail = "fail", strings.TrimSpace(id+" "+err.Error())
	}
	s.AuditManager.Record(audit.AuditEvent{Actor: trainingUser(r), Action: "training." + action,
		Resource: r.URL.Path, Result: result, IP: ClientIP(r), UserAgent: r.UserAgent(), Detail: detail})
}

// trainingError answers a failure with its status.
func trainingError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, errTrainingNotFound), errors.Is(err, fs.ErrNotExist), errors.Is(err, errTrainingNoResult):
		code = http.StatusNotFound
	case errors.Is(err, errTrainingInvalid), errors.Is(err, errDatasetInvalid), errors.Is(err, errDatasetLicence):
		code = http.StatusBadRequest
	case errors.Is(err, errTrainingNoTrainer), errors.Is(err, errTrainingBusy), errors.Is(err, errTrainingState):
		code = http.StatusConflict
	case errors.Is(err, errTrainingDisk):
		code = http.StatusInsufficientStorage
	}
	writeJSONStatus(w, code, map[string]string{"error": err.Error()})
}

// methodNotAllowed answers a method a route does not take.
func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// handleTrainingProfiles is GET /v1/training/profiles.
func (s *Server) handleTrainingProfiles(w http.ResponseWriter, r *http.Request, m *trainingManager) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	type view struct {
		*trainerProfile
		Running int `json:"running"`
	}
	list := make([]view, 0, len(m.profiles.list))
	for _, p := range m.profiles.list {
		list = append(list, view{trainerProfile: p, Running: m.runningOf(p.Name)})
	}
	errs := m.profiles.errors
	if errs == nil {
		errs = []string{}
	}
	ok(w, map[string]any{"configured": len(list) > 0, "profiles": list, "errors": errs})
}

// handleTrainingDatasets is GET (list) and POST (build) /v1/training/datasets.
func (s *Server) handleTrainingDatasets(w http.ResponseWriter, r *http.Request, m *trainingManager) {
	switch r.Method {
	case http.MethodGet:
		listTrainingJobs(w, m, trainingKindDataset)
	case http.MethodPost:
		var req datasetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			bad(w, err)
			return
		}
		j, err := m.submitDataset(req, trainingUser(r))
		s.auditTraining(r, "dataset", jobID(j), err)
		if err != nil {
			trainingError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusAccepted, map[string]any{"jobId": j.ID, "job": m.view(j)})
	default:
		methodNotAllowed(w)
	}
}

// handleTrainingJobs is GET (list) and POST (start) /v1/training/jobs.
func (s *Server) handleTrainingJobs(w http.ResponseWriter, r *http.Request, m *trainingManager) {
	switch r.Method {
	case http.MethodGet:
		listTrainingJobs(w, m, trainingKindRun)
	case http.MethodPost:
		var req runRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			bad(w, err)
			return
		}
		j, err := m.submitRun(req, trainingUser(r))
		s.auditTraining(r, "start", strings.TrimSpace(jobID(j)+" profile="+req.Profile), err)
		if err != nil {
			trainingError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusAccepted, map[string]any{"jobId": j.ID, "job": m.view(j)})
	default:
		methodNotAllowed(w)
	}
}

// jobID is a job's id, "" for none.
func jobID(j *trainingJob) string {
	if j == nil {
		return ""
	}
	return j.ID
}

// listTrainingJobs answers the jobs of a kind.
func listTrainingJobs(w http.ResponseWriter, m *trainingManager, kind string) {
	jobs, err := m.listJobs(kind)
	if err != nil {
		trainingError(w, err)
		return
	}
	ok(w, map[string]any{"jobs": jobs, "total": len(jobs)})
}

// handleTrainingDataset is /v1/training/datasets/{id}[/download].
func (s *Server) handleTrainingDataset(w http.ResponseWriter, r *http.Request, m *trainingManager) {
	id, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, trainingDatasetsPrefix), "/")
	switch {
	case action == "" && r.Method == http.MethodGet:
		showTrainingJob(w, m, id, trainingKindDataset)
	case action == "" && r.Method == http.MethodDelete:
		err := m.remove(id, trainingKindDataset)
		s.auditTraining(r, "delete", id, err)
		answerDeleted(w, id, err)
	case action == "download" && r.Method == http.MethodGet:
		s.downloadDataset(w, r, m, id)
	default:
		methodNotAllowed(w)
	}
}

// handleTrainingJob is /v1/training/jobs/{id}[/cancel|/evaluate|/download].
func (s *Server) handleTrainingJob(w http.ResponseWriter, r *http.Request, m *trainingManager) {
	id, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, trainingJobsPrefix), "/")
	switch {
	case action == "" && r.Method == http.MethodGet:
		showTrainingJob(w, m, id, trainingKindRun)
	case action == "" && r.Method == http.MethodDelete:
		err := m.remove(id, trainingKindRun)
		s.auditTraining(r, "delete", id, err)
		answerDeleted(w, id, err)
	case action == "cancel" && r.Method == http.MethodPost:
		j, err := m.cancel(id, trainingKindRun)
		s.auditTraining(r, "cancel", id, err)
		answerJob(w, m, j, err)
	case action == "evaluate" && r.Method == http.MethodPost:
		j, err := m.submitEvaluate(id)
		s.auditTraining(r, "evaluate", id, err)
		answerJob(w, m, j, err)
	case action == "download" && r.Method == http.MethodGet:
		s.downloadResult(w, r, m, id)
	default:
		methodNotAllowed(w)
	}
}

// showTrainingJob answers one job with the end of its log.
func showTrainingJob(w http.ResponseWriter, m *trainingManager, id, kind string) {
	j, err := m.jobOfKind(id, kind)
	if err != nil {
		trainingError(w, err)
		return
	}
	ok(w, map[string]any{"job": j, "log": m.logTail(id)})
}

// answerJob answers a job an action was accepted for.
func answerJob(w http.ResponseWriter, m *trainingManager, j *trainingJob, err error) {
	if err != nil {
		trainingError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]any{"jobId": j.ID, "job": m.view(j)})
}

// answerDeleted answers a delete.
func answerDeleted(w http.ResponseWriter, id string, err error) {
	if err != nil {
		trainingError(w, err)
		return
	}
	ok(w, map[string]any{"status": "deleted", "jobId": id})
}

// downloadDataset serves one part of a completed dataset.
func (s *Server) downloadDataset(w http.ResponseWriter, r *http.Request, m *trainingManager, id string) {
	part := r.URL.Query().Get("part")
	name := map[string]string{"": trainFile, "train": trainFile, "validation": validationFile}[part]
	j, err := m.jobOfKind(id, trainingKindDataset)
	if err == nil && name == "" {
		err = fmt.Errorf("%w: part is train or validation", errTrainingInvalid)
	}
	if err == nil && j.Status != BulkJobCompleted {
		err = fmt.Errorf("%w: the dataset is %s", errTrainingState, j.Status)
	}
	var f *os.File
	if err == nil {
		f, _, err = m.openJobFile(id, datasetSubdir+"/"+name)
	}
	s.auditTraining(r, "download", id+" "+name, err)
	if err != nil {
		trainingError(w, err)
		return
	}
	serveTrainingFile(w, r, f, id+"-"+name, "application/x-ndjson")
}

// downloadResult serves a run's result file.
func (s *Server) downloadResult(w http.ResponseWriter, r *http.Request, m *trainingManager, id string) {
	j, err := m.jobOfKind(id, trainingKindRun)
	var f *os.File
	if err == nil {
		f, _, err = m.openResult(j)
	}
	s.auditTraining(r, "download", id, err)
	if err != nil {
		trainingError(w, err)
		return
	}
	serveTrainingFile(w, r, f, j.Result.File, "application/octet-stream")
}

// serveTrainingFile streams an opened file as an attachment.
func serveTrainingFile(w http.ResponseWriter, r *http.Request, f *os.File, name, contentType string) {
	defer func() { _ = f.Close() }()
	liftWriteDeadline(w, r) // a model file outlasts the write timeout
	w.Header().Set(headerContentType, contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, f)
}
