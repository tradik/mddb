package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"time"

	"mddb/internal/audit"
	json "mddb/internal/jsonx"
	"mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// The parts of the diagnose bundle (OPS-025), one function each. Each
// returns the file's bytes; diagnose.go assembles them and redacts nothing
// itself — every part is built already redacted.

// diagnoseBuild is build.json.
type diagnoseBuild struct {
	Version         string `json:"version"`
	Commit          string `json:"commit,omitempty"`
	Modified        bool   `json:"modified,omitempty"`
	GoVersion       string `json:"goVersion"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	BuildTags       string `json:"buildTags,omitempty"`
	VectorMath      string `json:"vectorMath"` // avx2, neon or scalar (noasm builds)
	NumCPU          int    `json:"numCPU"`
	GOMAXPROCS      int    `json:"gomaxprocs"`
	UptimeSeconds   int64  `json:"uptimeSeconds"`
	StorageLayout   string `json:"storageLayout"`
	Mode            string `json:"mode"`
	ReplicationRole string `json:"replicationRole,omitempty"`
	Ready           bool   `json:"ready"`
}

// readBuildInfo is debug.ReadBuildInfo; a test replaces it.
var readBuildInfo = debug.ReadBuildInfo

// buildPart is build.json: what was built, where it runs, and how.
func (s *Server) buildPart() diagnoseBuild {
	b := diagnoseBuild{Version: VERSION, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		VectorMath: vector.MathTier(), NumCPU: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0),
		UptimeSeconds: int64(time.Since(serverStartTime).Seconds()), StorageLayout: "single-file",
		Mode: string(s.Mode), ReplicationRole: s.ReplicationRole, Ready: s.Ready}
	if s.collectionStore() != nil {
		b.StorageLayout = "per-collection"
	}
	if info, ok := readBuildInfo(); ok {
		for _, st := range info.Settings {
			switch st.Key {
			case "vcs.revision":
				b.Commit = st.Value
			case "vcs.modified":
				b.Modified = st.Value == "true"
			case "-tags":
				b.BuildTags = st.Value
			}
		}
	}
	return b
}

// knownCollections is every collection the bundle may mention: configured,
// counted, stored in a file of its own, or seen by the query-shape counters.
func (s *Server) knownCollections() []string {
	seen := map[string]bool{}
	if s.CollectionManager != nil {
		for c := range s.CollectionManager.ListAll() {
			seen[c] = true
		}
	}
	if snap := s.dbStats(statsWait()); snap != nil {
		for c := range snap.Collections {
			seen[c] = true
		}
	}
	if cf := s.collectionStore(); cf != nil {
		for _, c := range cf.names() {
			seen[c] = true
		}
	}
	for c := range s.Metrics.QueryShape().Report(nil).Collections {
		seen[c] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// diagnoseCollectionConfig is one collection's configuration in
// config.json: switches and numbers, never descriptions, prompts or paths.
type diagnoseCollectionConfig struct {
	Type               string `json:"type,omitempty"`
	StorageBackend     string `json:"storageBackend,omitempty"`
	Quantization       string `json:"quantization,omitempty"`
	DiskOnlyVectors    bool   `json:"diskOnlyVectors,omitempty"`
	Encrypted          bool   `json:"encrypted,omitempty"`
	ReadOnly           bool   `json:"readOnly,omitempty"`
	Corpus             bool   `json:"corpus,omitempty"`
	FTSExcludeMetaKeys int    `json:"ftsExcludeMetaKeys,omitempty"` // how many, not which
	MaxTopK            int    `json:"maxTopK,omitempty"`
	MaxRevisions       int    `json:"maxRevisions,omitempty"`
	Quota              bool   `json:"quota,omitempty"`
	EvalQueries        int    `json:"evalQueries,omitempty"`
}

// diagnoseConfig is config.json.
type diagnoseConfig struct {
	Server      ConfigResponse                      `json:"server"`
	Settings    map[string]string                   `json:"settings"`
	Collections map[string]diagnoseCollectionConfig `json:"collections"`
}

// configPart is config.json: the effective configuration through the
// allowlist, the variables that are set, and each collection's settings.
func (s *Server) configPart(names *bundleNames) diagnoseConfig {
	out := diagnoseConfig{Server: s.configResponse(audienceBundle), Settings: configSettings(audienceBundle),
		Collections: map[string]diagnoseCollectionConfig{}}
	if s.CollectionManager == nil {
		return out
	}
	for c, cfg := range s.CollectionManager.ListAll() {
		d := diagnoseCollectionConfig{Type: cfg.Type, StorageBackend: cfg.StorageBackend, Quantization: cfg.Quantization,
			DiskOnlyVectors: cfg.DiskOnlyVectors, Encrypted: cfg.Encrypted, ReadOnly: cfg.ReadOnly, Corpus: cfg.Corpus != nil,
			MaxRevisions: cfg.MaxRevisions, Quota: cfg.Quota != nil, EvalQueries: len(cfg.EvalSet)}
		if cfg.FTS != nil {
			d.FTSExcludeMetaKeys = len(cfg.FTS.ExcludeMetaKeys)
		}
		if cfg.Retrieval != nil {
			d.MaxTopK = cfg.Retrieval.MaxTopK
		}
		out.Collections[names.name(c)] = d
	}
	return out
}

// handlerPart calls an existing read-only handler in process, as the
// request's caller, and returns its JSON answer with names hashed and free
// strings replaced. The handlers stay the one source of these numbers.
func (s *Server) handlerPart(ctx context.Context, h http.HandlerFunc, path string, names *bundleNames) ([]byte, error) {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", path, rec.Code)
	}
	var v any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		return nil, err
	}
	return jsonPart(names.scrubJSON("", v), nil)
}

// metricsPart is metrics.prom: the /metrics page with its labels redacted.
func (s *Server) metricsPart(ctx context.Context, names *bundleNames) []byte {
	rec := httptest.NewRecorder()
	s.Metrics.HandleMetrics(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		// Metrics off: the query-shape counters still run, in query-shape.json.
		return []byte("# /metrics is disabled (MDDB_METRICS=false); query-shape.json carries the counters\n")
	}
	return []byte(names.scrubMetrics(rec.Body.String()))
}

// diagnoseErrors is errors.json.
type diagnoseErrors struct {
	Total   uint64       `json:"total"`
	Kept    int          `json:"kept"`
	Entries []errorEntry `json:"entries"`
}

func errorsPart() diagnoseErrors {
	entries, total := diagErrors.snapshot()
	return diagnoseErrors{Total: total, Kept: len(entries), Entries: entries}
}

// auditToken is an audit action or result the summary repeats: a code-defined
// identifier. Anything else counts as "other".
var auditToken = regexp.MustCompile(`^[a-z0-9_.:-]{1,48}$`)

// auditWindow is how far back the audit summary counts, and auditLimit the
// most events it reads.
const (
	auditWindow = 7 * 24 * time.Hour
	auditLimit  = 100_000
)

// diagnoseAudit is audit-summary.json.
type diagnoseAudit struct {
	WindowDays int                       `json:"windowDays"`
	Events     int                       `json:"events"`
	Truncated  bool                      `json:"truncated"`
	Counts     map[string]map[string]int `json:"counts"` // action → result → n
}

// auditPart counts the last week's audit events by action and result. The
// events carry actors, IPs, keys and details; none of them is read here.
func (s *Server) auditPart() (diagnoseAudit, error) {
	events, err := s.AuditManager.Query(audit.QueryFilter{FromNanos: time.Now().Add(-auditWindow).UnixNano(), Limit: auditLimit})
	out := diagnoseAudit{WindowDays: int(auditWindow / (24 * time.Hour)), Events: len(events),
		Truncated: len(events) >= auditLimit, Counts: map[string]map[string]int{}}
	for _, ev := range events {
		action, result := ev.Action, ev.Result
		if !auditToken.MatchString(action) {
			action = "other"
		}
		if !auditToken.MatchString(result) {
			result = "other"
		}
		nested(out.Counts, action)[result]++
	}
	return out, err
}

// nested returns m[k], creating it.
func nested[V any](m map[string]map[string]V, k string) map[string]V {
	if m[k] == nil {
		m[k] = map[string]V{}
	}
	return m[k]
}

// integrityFile is one database file's check.
type integrityFile struct {
	File     string `json:"file"` // "main", or a collection's bundle name
	Problems int    `json:"problems"`
	OK       bool   `json:"ok"`
}

// diagnoseIntegrity is integrity.json.
type diagnoseIntegrity struct {
	Files  []integrityFile `json:"files"`
	TookMs int64           `json:"tookMs"`
}

// integrityPart runs bbolt's consistency check over every open database
// file, in a read transaction each. It reports how many problems each file
// has and not what they are: bbolt's messages can quote keys.
func (s *Server) integrityPart(ctx context.Context, names *bundleNames) (diagnoseIntegrity, error) {
	start := time.Now()
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	files := []struct {
		name string
		db   *bolt.DB
	}{{"main", s.DB}}
	if cf := s.collectionStore(); cf != nil {
		all := cf.all()
		for _, c := range cf.names() {
			files = append(files, struct {
				name string
				db   *bolt.DB
			}{names.name(c), all[c]})
		}
	}
	out := diagnoseIntegrity{Files: []integrityFile{}}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		problems := 0
		err := f.db.View(func(tx *bolt.Tx) error {
			problems = countProblems(tx.Check())
			return nil
		})
		if err != nil {
			return out, err
		}
		out.Files = append(out.Files, integrityFile{File: f.name, Problems: problems, OK: problems == 0})
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// countProblems drains a consistency check to the end — bbolt blocks if its
// channel is left — and counts what it found, reading no message.
func countProblems(problems <-chan error) int {
	n := 0
	for range problems {
		n++
	}
	return n
}

// profilePart writes a runtime profile: goroutine stacks as text (debug 1:
// function names and file:line, no argument values) or the heap profile.
func profilePart(name string, debugLevel int) ([]byte, error) {
	var buf bytes.Buffer
	err := pprof.Lookup(name).WriteTo(&buf, debugLevel)
	return buf.Bytes(), err
}
