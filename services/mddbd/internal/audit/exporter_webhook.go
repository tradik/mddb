package audit

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"mddb/internal/httpclient"
	"net/http"
	"strings"
	"time"

	json "mddb/internal/jsonx"
)

// WebhookExporter delivers each audit event as a JSON POST to a fixed
// URL. Headers are set once at construction time so SIEM tokens
// (Splunk HEC, Datadog Logs API key, ELK Bearer) ride every request.
//
// Retry strategy mirrors the existing webhooks subsystem: 0s, 1s, 5s,
// 15s back-offs across four attempts. Failures past the last attempt
// increment the Failed counter and surface as LastError; the event
// is NOT requeued — the BoltDB audit trail remains the source of
// truth so anything missed here can be backfilled by /v1/audit.
type WebhookExporter struct {
	*exporterCore
	url     string
	headers []string // pre-parsed "Header: value" pairs
	client  *http.Client
}

// NewWebhookExporter builds an exporter from environment-derived
// inputs. headerCSV is "Authorization: Splunk xxx,X-MDDB-Source: prod"
// — comma-separated header pairs.
// webhookRetryBackoffs is the delay before each delivery attempt; the first is
// immediate. A variable rather than a literal so tests can shorten it — the
// full schedule adds 21 s to each of two tests that assert *that* a failure is
// counted, not how long the waiting takes (TEST-004).
var webhookRetryBackoffs = []time.Duration{0, 1 * time.Second, 5 * time.Second, 15 * time.Second}

func NewWebhookExporter(url, headerCSV string, bufSize int, insecureSkipTLSVerify bool) (*WebhookExporter, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("webhook url required")
	}
	core := newExporterCore("webhook", url, bufSize)
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: insecureSkipTLSVerify}, // #nosec G402 — opt-in via env
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 4,
	}
	w := &WebhookExporter{
		exporterCore: core,
		url:          url,
		headers:      parseHeaderCSV(headerCSV),
		client:       &http.Client{Timeout: 10 * time.Second, Transport: tr},
	}
	core.wg.Add(1)
	go core.run(w.deliver)
	return w, nil
}

// Export enqueues the event; non-blocking.
func (w *WebhookExporter) Export(ev AuditEvent) { w.pushOrDrop(ev) }

func (w *WebhookExporter) deliver(ev AuditEvent) error {
	payload, err := json.Marshal(struct {
		AuditEvent
		Type string `json:"_mddb_event_type"`
	}{AuditEvent: ev, Type: "audit"})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	backoffs := webhookRetryBackoffs
	var lastErr error
	for attempt, backoff := range backoffs {
		if backoff > 0 {
			time.Sleep(backoff)
		}
		// noctx would have this carry a context, which would let shutdown
		// interrupt the backoff sleeps. Doing that means threading one through
		// exporterCore.run, whose signature the syslog exporter shares — a
		// wider change than this ticket, tracked as a follow-up.
		req, err := http.NewRequest(http.MethodPost, w.url, bytes.NewReader(payload)) //nolint:noctx // see above
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-MDDB-Event", "audit")
		for _, h := range w.headers {
			parts := strings.SplitN(h, ":", 2)
			if len(parts) == 2 {
				req.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
			}
		}
		resp, err := w.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d: %w", attempt+1, err)
			continue
		}
		httpclient.DrainAndClose(resp.Body)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("attempt %d: HTTP %d", attempt+1, resp.StatusCode)
	}
	return lastErr
}

// parseHeaderCSV splits "K1: v1,K2: v2" into ["K1: v1", "K2: v2"].
// Empty input returns an empty slice. Whitespace around each entry
// is trimmed; bad entries (no colon) are dropped silently.
func parseHeaderCSV(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.Contains(p, ":") {
			out = append(out, p)
		}
	}
	return out
}
