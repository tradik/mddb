package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	json "mddb/internal/jsonx"
	pb "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #333: with the freelist deferred the database opens at once, reads work,
// and writes are refused on every transport until it is rebuilt.

func TestBackgroundOpenServesReadsAndHoldsWrites(t *testing.T) {
	path := newWalkingDB(t)
	t.Setenv("MDDB_FREELIST_BACKGROUND", "")
	t.Setenv("MDDB_PREWARM", "on")
	t.Setenv("MDDB_FREELIST_SYNC", "true")
	db, walked, bg, err := openDatabase(newStartupClock(), path, getOptimizedBoltOptions())
	if err != nil || walked || bg == nil {
		t.Fatalf("walked=%v bg=%v err=%v", walked, bg, err)
	}
	defer func() { _ = db.Close() }()
	if db.FreelistLoaded() || !bg.loading() {
		t.Fatal("the freelist was loaded at open")
	}
	s := &Server{DB: db, freelistLoad: bg}
	if err := s.writeBlocked(); !errors.Is(err, errFreelistLoading) {
		t.Fatalf("writeBlocked = %v", err)
	}
	body := map[string]any{"status": "healthy"}
	s.healthFreelist(body)
	if body["status"] != "degraded" || body["writes"] != false || body["reads"] != true {
		t.Errorf("health while loading: %v", body)
	}

	bg.run(db, path, bg.meta) // what start() runs in the background
	if bg.loading() || !db.FreelistLoaded() || s.writeBlocked() != nil {
		t.Fatalf("after the rebuild: loading=%v err=%v", bg.loading(), s.writeBlocked())
	}
	if m, _ := readBoltMeta(path); m.needsWalk() {
		t.Error("with freelist sync on, the rebuilt freelist should be stored at once")
	}
	body = map[string]any{"status": "healthy"}
	s.healthFreelist(body)
	if body["status"] != "healthy" {
		t.Errorf("health after: %v", body)
	}
}

func TestWritesAreRefusedOnEveryTransportWhileLoading(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	s.freelistLoad = &backgroundFreelist{clock: newStartupClock()}
	s.freelistLoad.clock.begin("rebuilding freelist")
	s.freelistLoad.clock.setProgress(10, 100)

	rec := httptest.NewRecorder()
	s.guardWrite(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })(rec,
		httptest.NewRequest(http.MethodPost, "/v1/add", nil))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || out["phase"] != "rebuilding freelist" || out["progress"] == nil {
		t.Errorf("HTTP: %d %q %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}

	_, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "c", Key: "k", Lang: "en", ContentMd: "x"})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("gRPC: %v", err)
	}

	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	if _, err := ts.mcpCallTool(context.Background(), "add_document", map[string]interface{}{"collection": "c", "key": "k", "lang": "en", "content_md": "x"}); !errors.Is(err, errFreelistLoading) {
		t.Errorf("MCP write: %v", err)
	}
	if _, err := ts.mcpCallTool(context.Background(), "get_stats", map[string]interface{}{}); errors.Is(err, errFreelistLoading) {
		t.Error("MCP read refused")
	}

	// A failed rebuild leaves the database read-only, and says why.
	s.freelistLoad.err = errors.New("bad page")
	s.freelistLoad.done.Store(true)
	if err := s.writeBlocked(); err == nil {
		t.Error("writes allowed after a failed rebuild")
	}
	body := map[string]any{}
	s.healthFreelist(body)
	if body["status"] != "degraded" || body["error"] == nil {
		t.Errorf("health after a failure: %v", body)
	}
	if (&Server{}).writeBlocked() != nil || (*backgroundFreelist)(nil).loading() {
		t.Error("a server without a rebuild")
	}
	(*backgroundFreelist)(nil).start()
}

func TestCustomToolsOfEveryActionAreReadOnly(t *testing.T) {
	var tools []MCPCustomToolConfig
	for action := range customToolActions {
		tools = append(tools, MCPCustomToolConfig{Name: "t_" + action, Action: action})
	}
	ts := &MCPToolServer{customTools: tools}
	for _, ct := range tools {
		if !ts.isToolReadOnly(ct.Name) {
			t.Errorf("custom tool wrapping %s is not read-only", ct.Action)
		}
	}
	if ts.isToolReadOnly("no_such_tool") {
		t.Error("unknown tool")
	}
}
