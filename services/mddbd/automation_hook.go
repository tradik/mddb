package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// POST /v1/automation/{id}/hook (#421): a forge's push or release webhook
// runs a git sync at once, through the code its schedule fires (RunNow). The
// forge has no mddb credentials, so the route is open (isAutomationHookPath)
// and the request proves itself instead, against the stored secret the rule
// names in hookSecretRef: GitHub's X-Hub-Signature-256 (also sent by Gitea
// and Forgejo), Gitea's and Forgejo's own signature headers, or GitLab's
// X-Gitlab-Token. A rule without a hook answers 404, as an unknown id does.

// maxHookBodyBytes bounds a webhook body; the forges cap theirs at 25 MB.
const maxHookBodyBytes = 25 << 20

// The hook's errors, each answered with its own status.
var (
	errHookNotFound  = errors.New("no hook for this id")
	errHookSignature = errors.New("the webhook signature does not match")
)

// isAutomationHookPath reports whether path is /v1/automation/{id}/hook,
// which the auth middleware and the access gate let through: the handler
// verifies the request's signature instead.
func isAutomationHookPath(p string) bool {
	id, ok := strings.CutPrefix(p, "/v1/automation/")
	id, ok2 := strings.CutSuffix(id, "/hook")
	return ok && ok2 && id != "" && !strings.Contains(id, "/")
}

// handleAutomationHook verifies a webhook and runs the rule's sync: 202 with
// the run, 401 for a bad signature, 404 for no hook, 409 while the previous
// run is going.
func (s *Server) handleAutomationHook(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	s.Metrics.IncOp("automation_hook", id)
	status, body := s.automationHook(r, id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// automationHook is the hook's answer.
func (s *Server) automationHook(r *http.Request, id string) (int, any) {
	secret, err := s.hookSecret(id)
	if err != nil {
		if errors.Is(err, errHookNotFound) {
			return http.StatusNotFound, hookError(err.Error())
		}
		return http.StatusServiceUnavailable, hookError("the hook's secret cannot be opened")
	}
	if err := verifyHook(r, secret); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return http.StatusRequestEntityTooLarge, hookError("the webhook body is too large")
		}
		return http.StatusUnauthorized, hookError(err.Error())
	}
	if r.Header.Get("X-GitHub-Event") == "ping" {
		return http.StatusOK, map[string]string{"status": "pong"}
	}
	run, err := s.runAutomationNow(id)
	if err != nil {
		return automationRunStatus(err), hookError(err.Error())
	}
	return http.StatusAccepted, run
}

// hookError is the body of a refused hook.
func hookError(msg string) map[string]string { return map[string]string{"error": msg} }

// hookSecret opens the secret a rule's hook is verified with.
func (s *Server) hookSecret(id string) (string, error) {
	rule := s.AutomationManager.stored(id)
	if rule == nil || rule.Sync == nil || rule.Sync.HookSecretRef == "" {
		return "", errHookNotFound
	}
	return s.syncSecret(rule.Sync.HookSecretRef)
}

// hookSignatures are the headers an HMAC-SHA256 of the body arrives in, and
// their prefix.
var hookSignatures = []struct{ header, prefix string }{
	{"X-Hub-Signature-256", "sha256="},
	{"X-Gitea-Signature", ""},
	{"X-Forgejo-Signature", ""},
}

// verifyHook checks the request against the secret: a GitLab token, or an
// HMAC-SHA256 of the whole body.
func verifyHook(r *http.Request, secret string) error {
	if token := r.Header.Get("X-Gitlab-Token"); token != "" {
		if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1 {
			return nil
		}
		return errHookSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if err := hashBody(mac, r.Body); err != nil {
		return err
	}
	sum := mac.Sum(nil)
	for _, sig := range hookSignatures {
		got, ok := strings.CutPrefix(r.Header.Get(sig.header), sig.prefix)
		want, err := hex.DecodeString(got)
		if ok && got != "" && err == nil && hmac.Equal(want, sum) {
			return nil
		}
	}
	return errHookSignature
}

// hashBody streams a body of at most maxHookBodyBytes into h.
func hashBody(h hash.Hash, body io.Reader) error {
	n, err := io.Copy(h, io.LimitReader(body, maxHookBodyBytes+1))
	if err != nil {
		return err
	}
	if n > maxHookBodyBytes {
		return &http.MaxBytesError{Limit: maxHookBodyBytes}
	}
	return nil
}
