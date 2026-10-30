package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"mddb/internal/audit"
	json "mddb/internal/jsonx"
	"mddb/internal/metrics"
)

// OPS-025: the diagnose bundle's privacy contract. Unique strings are seeded
// everywhere a user's data or identity lives — document content, a metadata
// value, a document key, query text, API keys, a header list of internal
// addresses, a path, a collection name, an actor and an IP — and the bundle
// is searched, every file, including the gunzipped heap profile and the
// metrics page, for any of them.

// Canaries. The collection, key and query canaries are lowercase on purpose:
// they then look like the identifiers metric labels may keep, so only the
// collection hashing and the label rules stand between them and the bundle.
const (
	canaryCollection = "canarycollqx7"
	canaryContent    = "CanaryContentQx7 body text"
	canaryMetaValue  = "CanaryMetaValueQx7"
	canaryKey        = "canarykeyqx7"
	canaryQuery      = "canaryqueryqx7"
	canaryAPIKey     = "CanaryApiKeyQx7" // #nosec G101 -- a test canary, not a credential
	canaryMCPKeys    = "CanaryMcpKeyQx7"
	canaryProxy      = "198.51.100.77"
	canaryPath       = "/srv/canarypathqx7"
	canaryActor      = "canaryactorqx7"
	canaryIP         = "203.0.113.77"
	canaryUnknownEnv = "CanaryUnknownValueQx7"
)

// diagnoseServer is corpusServer with metrics on, the audit log, aliases and
// the /es surface, behind main's HTTP chain.
func diagnoseServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv(esCompatEnv, "true")
	s := corpusServer(t, 256)
	s.Aliases = NewAliasManager(s.DB)
	if err := s.Aliases.LoadAll(); err != nil {
		t.Fatal(err)
	}
	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})
	am := audit.NewAuditManager(s.DB, true, 90)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	am.Start()
	t.Cleanup(am.Stop)
	s.AuditManager = am
	return s, esHandler(t, s, false)
}

// serveAs sends a request from the canary IP through h.
func serveAs(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = canaryIP + ":40000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+canaryAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// seedCanaries writes the canary collection and runs a search of every mode
// and surface the counters see, plus requests that fail, so every part of
// the bundle has something to say about it.
func seedCanaries(t *testing.T, s *Server, h http.Handler) {
	t.Helper()
	doc := addTestDoc(t, s, canaryCollection, canaryKey, "en", "# Notes\n\n"+canaryContent+" "+canaryQuery,
		map[string][]string{"tag": {canaryMetaValue}})
	waitForEmbedding(t, s, canaryCollection, doc.ID)
	if err := s.CollectionManager.Set(canaryCollection, &CollectionConfig{Type: "documents", Description: canaryContent,
		ResponsePrompt: canaryContent, FTS: &CollectionFTSDef{ExcludeMetaKeys: []string{canaryQuery}},
		Retrieval: &RetrievalProfileDef{MaxTopK: 50}}); err != nil {
		t.Fatal(err)
	}
	filter := `"filterMeta":{"tag":["` + canaryMetaValue + `"]}`
	for _, rq := range []struct{ path, body string }{
		{"/v1/fts", `{"collection":"` + canaryCollection + `","query":"` + canaryQuery + `",` + filter + `}`},
		{"/v1/vector-search", `{"collection":"` + canaryCollection + `","query":"` + canaryQuery + `","algorithm":"auto",` + filter + `}`},
		{"/v1/hybrid-search", `{"collection":"` + canaryCollection + `","query":"` + canaryQuery + `",` + filter + `}`},
		{"/v1/search", `{"collection":"` + canaryCollection + `",` + filter + `}`},
		{"/v1/code-graph", `{"collection":"` + canaryCollection + `","key":"` + canaryKey + `","depth":9}`},
		{"/v1/code-graph", `{"collection":"` + canaryCollection + `"}`},
		{"/es/" + canaryCollection + "/_search", `{"query":{"match":{"content":"` + canaryQuery + `"}}}`},
		// Fails: the error ring records it, with the caller's operator in
		// the message.
		{"/v1/fts", `{"collection":"` + canaryCollection + `","query":"q","filter":{"tag":{"` + canaryQuery + `":"x"}}}`},
	} {
		serveAs(t, h, http.MethodPost, rq.path, rq.body)
	}
	serveAs(t, h, http.MethodGet, "/v1/aliases/"+canaryCollection, "")
	mcp := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	if _, err := mcp.mcpCallTool(context.Background(), "full_text_search", map[string]any{"collection": canaryCollection, "query": canaryQuery}); err != nil {
		t.Fatal(err)
	}
	s.AuditManager.Record(audit.AuditEvent{Actor: canaryActor, Action: "document.add", Collection: canaryCollection,
		Key: canaryKey, IP: canaryIP, UserAgent: canaryActor, Detail: canaryContent})
	// An action or result that is not an identifier counts as "other".
	s.AuditManager.Record(audit.AuditEvent{Actor: canaryActor, Action: "document.add", Result: canaryContent})
	waitFor(t, "the audit event", func() bool {
		ev, _ := s.AuditManager.Query(audit.QueryFilter{Actor: canaryActor})
		return len(ev) == 2
	})
}

// setCanaryEnv puts the configuration canaries in the environment.
func setCanaryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MDDB_EMBEDDING_API_KEY", canaryAPIKey)
	t.Setenv("MDDB_MCP_API_KEYS", canaryMCPKeys)
	t.Setenv("MDDB_TRUSTED_PROXIES", canaryProxy+",10.9.8.7")
	t.Setenv("MDDB_BACKUP_DIR", canaryPath)
	t.Setenv("MDDB_EMBEDDING_API_URL", "https://"+canaryActor+":"+canaryAPIKey+"@"+canaryPath[1:]+".example/v1")
	t.Setenv("MDDB_CANARY_UNKNOWN", canaryUnknownEnv)
}

// getBundle fetches the bundle through h and unpacks it.
func getBundle(t *testing.T, h http.Handler, query string) (map[string][]byte, []string) {
	t.Helper()
	rec := serveAs(t, h, http.MethodGet, "/v1/admin/diagnose"+query, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("diagnose = %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	return untarBundle(t, rec.Body.Bytes())
}

// untarBundle returns a bundle's files and their order.
func untarBundle(t *testing.T, b []byte) (map[string][]byte, []string) {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	files, order := map[string][]byte{}, []string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files, order
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[hdr.Name], order = body, append(order, hdr.Name)
	}
}

// readable is a file as the canary search reads it: a gzip stream (the heap
// profile) decompressed.
func readable(t *testing.T, body []byte) string {
	t.Helper()
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	return string(body)
}

// manifestOf decodes MANIFEST.json.
func manifestOf(t *testing.T, files map[string][]byte) diagnoseManifest {
	t.Helper()
	var man diagnoseManifest
	if err := json.Unmarshal(files["MANIFEST.json"], &man); err != nil {
		t.Fatal(err)
	}
	return man
}

func TestDiagnoseBundleCarriesNoCanary(t *testing.T) {
	for _, layout := range corpusLayouts {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			setCanaryEnv(t)
			s, h := diagnoseServer(t)
			seedCanaries(t, s, h)
			canaries := []string{canaryCollection, canaryContent, canaryMetaValue, canaryKey, canaryQuery, canaryAPIKey,
				canaryMCPKeys, canaryProxy, canaryPath, canaryActor, canaryIP, canaryUnknownEnv, filepath.Base(s.Path)}

			// Positive controls: each canary is there to be found, so a clean
			// bundle is redaction at work and not an empty source.
			rawMetrics := serveAs(t, h, http.MethodGet, "/metrics", "").Body.String()
			rawStats := serveAs(t, h, http.MethodGet, "/v1/stats", "").Body.String()
			for _, c := range []string{canaryCollection} {
				if !strings.Contains(rawMetrics, c) || !strings.Contains(rawStats, c) {
					t.Fatalf("the sources do not name %q: the test proves nothing", c)
				}
			}
			if !strings.Contains(rawStats, filepath.Base(s.Path)) || !strings.Contains(rawMetrics, "/v1/aliases/"+canaryCollection) {
				t.Fatal("/v1/stats lost its path or /metrics its per-request path: the test proves nothing")
			}

			files, order := getBundle(t, h, "?profiles=true&integrity=true")
			for _, want := range []string{"MANIFEST.json", "build.json", "config.json", "env.json", "stats.json", "storage.json",
				"vector-stats.json", "indexing.json", "metrics.prom", "query-shape.json", "audit-summary.json", "errors.json",
				"goroutines.pprof", "heap.pprof", "integrity.json"} {
				if files[want] == nil {
					t.Errorf("bundle lacks %s (has %v)", want, order)
				}
			}
			for name, body := range files {
				text := readable(t, body)
				for _, c := range canaries {
					if strings.Contains(text, c) {
						t.Errorf("%s carries canary %q", name, c)
					}
				}
			}

			// The data the canaries stood in is still described: hashed,
			// counted.
			hash := collectionHash(canaryCollection)
			for _, f := range []string{"stats.json", "metrics.prom", "query-shape.json", "integrity.json"} {
				oneFile := f == "integrity.json" && layout == "single" // no collection file to name
				if !oneFile && !strings.Contains(string(files[f]), hash) {
					t.Errorf("%s does not mention the collection's hash %s", f, hash)
				}
			}
			var cfg diagnoseConfig
			_ = json.Unmarshal(files["config.json"], &cfg)
			if c := cfg.Collections[hash]; c.Type != "documents" || c.FTSExcludeMetaKeys != 1 || c.MaxTopK != 50 {
				t.Errorf("config.json collection %+v", cfg.Collections)
			}
			var env map[string]string
			_ = json.Unmarshal(files["env.json"], &env)
			if env["MDDB_EMBEDDING_API_KEY"] != cfgSet || env["MDDB_TRUSTED_PROXIES"] != "<2 entries>" ||
				env["MDDB_CANARY_UNKNOWN"] != cfgUnclassified || env["MDDB_EMBEDDING_API_URL"] != "<set: https>" {
				t.Errorf("env.json %v", env)
			}

			// --keep-names keeps the collection's name and nothing else.
			kept, _ := getBundle(t, h, "?keepNames=true")
			found := false
			for name, body := range kept {
				found = found || strings.Contains(string(body), canaryCollection)
				for _, c := range canaries[1:] {
					if strings.Contains(string(body), c) {
						t.Errorf("keepNames: %s carries canary %q", name, c)
					}
				}
			}
			if !found {
				t.Error("keepNames: the collection's name is in no file")
			}
		})
	}
}

func TestDiagnoseManifestDescribesEveryFile(t *testing.T) {
	s, h := diagnoseServer(t)
	seedCanaries(t, s, h)
	start := time.Now()
	rec := serveAs(t, h, http.MethodGet, "/v1/admin/diagnose", "")
	took := time.Since(start)
	files, order := untarBundle(t, rec.Body.Bytes())
	if order[0] != "MANIFEST.json" {
		t.Fatalf("first file %q", order[0])
	}
	// The default bundle: small and quick (OPS-025 acceptance).
	if n := rec.Body.Len(); n >= 1<<20 || took >= 2*time.Second {
		t.Errorf("default bundle %d bytes in %s", n, took)
	}
	man := manifestOf(t, files)
	if len(man.Files) != len(files)-1 || len(man.Never) == 0 || man.Version != VERSION {
		t.Fatalf("manifest lists %d of %d files: %+v", len(man.Files), len(files)-1, man)
	}
	for _, f := range man.Files {
		sum := sha256.Sum256(files[f.Name])
		if f.Size != len(files[f.Name]) || f.SHA256 != hex.EncodeToString(sum[:]) || f.Redaction == "" {
			t.Errorf("manifest entry %+v does not match the file (%d bytes)", f, len(files[f.Name]))
		}
		t.Logf("%-20s %7d bytes", f.Name, f.Size)
	}
	omitted := map[string]string{}
	for _, o := range man.Omitted {
		omitted[o.Name] = o.Reason
	}
	if omitted["goroutines.pprof"] != "opt-in: profiles" || omitted["heap.pprof"] != "opt-in: profiles" ||
		omitted["integrity.json"] != "opt-in: integrity" {
		t.Errorf("omitted %v", omitted)
	}
	var shape metrics.QueryShapeReport
	if err := json.Unmarshal(files["query-shape.json"], &shape); err != nil {
		t.Fatal(err)
	}
	if shape.Searches["rest"]["fts"].Total == 0 || shape.Searches["mcp"]["fts"].Total == 0 || shape.Searches["es"]["fts"].Total == 0 ||
		shape.Searches["rest"]["vector"].Total == 0 || shape.Searches["rest"]["hybrid"].Total == 0 || shape.Searches["rest"]["meta"].Total == 0 ||
		shape.Graph.DepthClamped["rest"]["code_graph"] != 1 || shape.Graph.NoStart["rest"] != 1 {
		t.Errorf("query shape %+v", shape)
	}
	var errs diagnoseErrors
	_ = json.Unmarshal(files["errors.json"], &errs)
	if errs.Kept == 0 || errs.Entries[len(errs.Entries)-1].Site == "" {
		t.Errorf("errors.json %+v", errs)
	}
}

func TestDiagnoseIsAdminOnlyAndReadOnlySafe(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if rec := doGet(s.handleDiagnose, http.MethodPost, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
	s.AuthManager = &AuthManager{enabled: true}
	if rec := doGet(s.handleDiagnose, http.MethodGet, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims = %d", rec.Code)
	}
	user := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "u"})
	rec := httptest.NewRecorder()
	s.handleDiagnose(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/diagnose", nil).WithContext(user))
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin = %d", rec.Code)
	}

	// A read-only follower answers: nothing in the bundle writes.
	s.AuthManager, s.Mode, s.ReplicationRole = nil, ModeRead, "follower"
	rec = doGet(s.handleDiagnose, http.MethodGet, "?integrity=true")
	files, _ := untarBundle(t, rec.Body.Bytes())
	var build diagnoseBuild
	_ = json.Unmarshal(files["build.json"], &build)
	if rec.Code != http.StatusOK || build.ReplicationRole != "follower" || build.Mode != string(ModeRead) || files["integrity.json"] == nil {
		t.Fatalf("read-only follower: %d %+v", rec.Code, build)
	}
	man := manifestOf(t, files)
	if man.Omitted[0].Name != "audit-summary.json" || man.Omitted[0].Reason != "audit log disabled" {
		t.Errorf("omitted %+v", man.Omitted)
	}
}

// doGet calls h with method and a query string, as an admin-less caller.
func doGet(h http.HandlerFunc, method, query string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(method, "/v1/admin/diagnose"+query, nil))
	return rec
}

// diagnoseGoneClient accepts nothing, like a client that went away.
type diagnoseGoneClient struct{ http.ResponseWriter }

func (diagnoseGoneClient) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestDiagnoseOmitsWhatFailsOrIsTooLarge(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	// A client that went away: the handler logs and returns.
	s.handleDiagnose(diagnoseGoneClient{httptest.NewRecorder()}, httptest.NewRequest(http.MethodGet, "/v1/admin/diagnose", nil))

	// Metrics off: the page says where the counters went.
	s.Metrics = metrics.NewMetrics(false, nil)
	names := newBundleNames(nil, false)
	if b := s.metricsPart(context.Background(), names); !strings.Contains(string(b), "disabled") {
		t.Errorf("metrics off: %q", b)
	}
	// A cancelled request: the integrity check and the handlers stop.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.integrityPart(ctx, names); err == nil {
		t.Error("integrity ran under a cancelled context")
	}
	if _, err := s.handlerPart(context.Background(), func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, "/x", names); err == nil {
		t.Error("a 500 was taken as an answer")
	}
	if _, err := s.handlerPart(context.Background(), func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }, "/x", names); err == nil {
		t.Error("broken JSON was taken as an answer")
	}

	// Through the bundle: a failing part and an oversized one are listed as
	// omitted, with the reason.
	var out bytes.Buffer
	_ = s.DB.Close() // integrity and the handlers now fail
	if err := s.writeDiagnoseBundle(context.Background(), &out, diagnoseOptions{Integrity: true}); err != nil {
		t.Fatal(err)
	}
	files, _ := untarBundle(t, out.Bytes())
	man := manifestOf(t, files)
	reasons := map[string]string{}
	for _, o := range man.Omitted {
		reasons[o.Name] = o.Reason
	}
	if !strings.HasPrefix(reasons["integrity.json"], "failed: ") {
		t.Errorf("omitted %v", reasons)
	}
	big := diagnosePart{name: "big", build: func(context.Context) ([]byte, error) { return make([]byte, maxDiagnoseFile+1), nil }}
	if got := runPart(context.Background(), big); got.reason != "too large" {
		t.Errorf("oversized part: %+v", got)
	}
	if err := s.writeDiagnoseBundle(context.Background(), diagnoseGoneClient{}, diagnoseOptions{}); err == nil {
		t.Error("a failed write was not reported")
	}
}

func TestDiagnosePartsEdgeCases(t *testing.T) {
	// bbolt's findings are counted, never read.
	problems := make(chan error, 2)
	problems <- errors.New("page 7: key " + canaryKey)
	problems <- errors.New("page 8")
	close(problems)
	if n := countProblems(problems); n != 2 {
		t.Errorf("countProblems = %d", n)
	}

	// The build's VCS facts and tags, when the binary carries them.
	orig := readBuildInfo
	t.Cleanup(func() { readBuildInfo = orig })
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.modified", Value: "true"}, {Key: "-tags", Value: "noasm"}, {Key: "GOOS", Value: "linux"}}}, true
	}
	s := &Server{Mode: ModeRead}
	if b := s.buildPart(); b.Commit != "abc123" || !b.Modified || b.BuildTags != "noasm" || b.StorageLayout != "single-file" {
		t.Errorf("build %+v", b)
	}

	// No collection manager: no collections, and no failure.
	if c := s.configPart(newBundleNames(nil, false)); len(c.Collections) != 0 || c.Settings == nil {
		t.Errorf("config %+v", c)
	}

	// An audit action that is not an identifier counts as "other".
	db, cleanup := newHandlerTestServer(t)
	defer cleanup()
	am := audit.NewAuditManager(db.DB, true, 90)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	am.Start()
	defer am.Stop()
	am.Record(audit.AuditEvent{Action: "Login As " + canaryActor})
	waitFor(t, "the audit event", func() bool { ev, _ := am.Query(audit.QueryFilter{}); return len(ev) == 1 })
	s.AuditManager = am
	if sum, err := s.auditPart(); err != nil || sum.Counts["other"]["ok"] != 1 {
		t.Errorf("audit %+v %v", sum, err)
	}
}
