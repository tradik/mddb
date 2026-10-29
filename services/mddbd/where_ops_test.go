package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func whereServer(t *testing.T) (*Server, func()) {
	s, cleanup := newHandlerTestServer(t)
	for i := range 7 {
		kind := "log"
		if i%2 == 1 {
			kind = "page"
		}
		addTestDoc(t, s, "c", "doc"+itoa(i), "en", "body "+itoa(i), map[string][]string{"kind": {kind}})
	}
	return s, cleanup
}

func whereKeys(t *testing.T, s *Server) []string {
	t.Helper()
	page, _, err := s.searchDocuments("c", nil, "key", true, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, d := range page {
		keys = append(keys, d.Key)
	}
	return keys
}

func TestWhere_DeleteMatchesTheDryRunExactly(t *testing.T) {
	s, cleanup := whereServer(t)
	defer cleanup()
	logs := MetaFilter{"kind": {In: []string{"log"}, hasIn: true}}
	dry, err := s.RunWhere(WhereDelete, WhereRequest{Collection: "c", Filter: logs, DryRun: true})
	if err != nil || dry.Matched != 4 || dry.Affected != 4 || !dry.DryRun || len(whereKeys(t, s)) != 7 {
		t.Fatalf("dry run: %v %+v", err, dry)
	}
	res, err := s.RunWhere(WhereDelete, WhereRequest{Collection: "c", Filter: logs})
	if err != nil || res.Affected != 4 || strings.Join(res.Sample, ",") != strings.Join(dry.Sample, ",") {
		t.Fatalf("delete: %v %+v vs %+v", err, res, dry)
	}
	if got := whereKeys(t, s); strings.Join(got, ",") != "doc1,doc3,doc5" {
		t.Fatalf("left: %v", got)
	}
	// As a single delete does: revisions and the full-text entries go too.
	_ = s.DB.View(func(tx *bolt.Tx) error {
		if k, _ := tx.Bucket([]byte("rev")).Cursor().Seek(storage.RevPrefix("c", genID("c", "doc0", "en"))); k != nil &&
			strings.HasPrefix(string(k), string(storage.RevPrefix("c", genID("c", "doc0", "en")))) {
			t.Fatal("revisions of a deleted document remain")
		}
		return nil
	})
	if r, _ := s.searchFTS(FTSSearchRequest{Collection: "c", Query: "body"}, time.Now()); r.Total != 3 {
		t.Fatalf("fts still finds deleted documents: %d", r.Total)
	}
}

func TestWhere_LimitAndCursor(t *testing.T) {
	s, cleanup := whereServer(t)
	defer cleanup()
	all := MetaFilter{"kind": {Exists: boolPtr(true)}}
	seen := map[string]bool{}
	cursor := ""
	calls := 0
	for {
		res, err := s.RunWhere(WhereUpdate, WhereRequest{Collection: "c", Filter: all, Limit: 3, Cursor: cursor,
			Meta: map[string][]string{"owner": {"ann"}}})
		if err != nil || res.Affected > 3 {
			t.Fatalf("call %d: %v %+v", calls, err, res)
		}
		for _, k := range res.Sample {
			if seen[k] {
				t.Fatalf("%s updated twice", k)
			}
			seen[k] = true
		}
		calls++
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if calls != 3 || len(seen) != 7 {
		t.Fatalf("%d calls, %d documents", calls, len(seen))
	}
	// Merged, not replaced: kind stays.
	d, _ := s.loadDocByRef("c", "doc2", "en")
	if d.Meta["owner"][0] != "ann" || d.Meta["kind"][0] != "log" {
		t.Fatalf("merge: %+v", d.Meta)
	}

	ttl := int64(3600)
	res, err := s.RunWhere(WhereSetTTL, WhereRequest{Collection: "c", FilterMeta: map[string][]string{"kind": {"page"}}, TTL: &ttl})
	if err != nil || res.Affected != 3 {
		t.Fatalf("ttl: %v %+v", err, res)
	}
	if d, _ = s.loadDocByRef("c", "doc1", "en"); d.ExpiresAt == 0 {
		t.Fatal("ttl not set")
	}
}

func TestWhere_Validation(t *testing.T) {
	s, cleanup := whereServer(t)
	defer cleanup()
	f := MetaFilter{"kind": {In: []string{"log"}, hasIn: true}}
	neg := int64(-1)
	for name, c := range map[string]struct {
		op  WhereOp
		req WhereRequest
	}{
		"no collection":  {WhereDelete, WhereRequest{Filter: f}},
		"no filter":      {WhereDelete, WhereRequest{Collection: "c"}},
		"limit too high": {WhereDelete, WhereRequest{Collection: "c", Filter: f, Limit: maxWhereLimit + 1}},
		"no meta":        {WhereUpdate, WhereRequest{Collection: "c", Filter: f}},
		"no ttl":         {WhereSetTTL, WhereRequest{Collection: "c", Filter: f}},
		"negative ttl":   {WhereSetTTL, WhereRequest{Collection: "c", Filter: f, TTL: &neg}},
	} {
		if _, err := s.RunWhere(c.op, c.req); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A per-document failure is reported and the rest continue: a schema
	// that rejects the merged metadata.
	s.SchemaManager = nil
	if res, err := s.RunWhere(WhereUpdate, WhereRequest{Collection: "c", Filter: f,
		Meta: map[string][]string{"k": {strings.Repeat("v", 40000)}}}); err != nil || len(res.Errors) != 4 || res.Affected != 0 {
		t.Fatalf("errors: %v %+v", err, res)
	}
	// A matched document that disappeared is skipped.
	if s.whereDoc("c", "missing") != nil {
		t.Fatal("missing doc")
	}
}

func TestWhere_Surfaces(t *testing.T) {
	s, cleanup := whereServer(t)
	defer cleanup()
	call := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleWhere(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec
	}
	rec := call("/v1/documents:deleteWhere", `{"collection":"c","filterMeta":{"kind":["log"]},"dryRun":true}`)
	var res WhereResult
	if json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Matched != 4 {
		t.Fatalf("REST dry run: %s", rec.Body.String())
	}
	if rec = call("/v1/documents:updateWhere", `{"collection":"c","filterMeta":{"kind":["log"]},"meta":{"x":["1"]}}`); rec.Code != 200 {
		t.Fatalf("REST update: %d %s", rec.Code, rec.Body.String())
	}
	if rec = call("/v1/documents:setTTLWhere", `{"collection":"c"}`); rec.Code != 400 {
		t.Fatalf("REST invalid: %d", rec.Code)
	}
	if rec = call("/v1/documents:deleteWhere", `{`); rec.Code != 400 {
		t.Fatalf("REST bad json: %d", rec.Code)
	}
	r := httptest.NewRecorder()
	s.handleWhere(r, httptest.NewRequest(http.MethodGet, "/v1/documents:deleteWhere", nil))
	if r.Code != http.StatusMethodNotAllowed {
		t.Fatal("method")
	}
	// Read-only: a dry run is answered, a write refused.
	s.Mode = ModeRead
	if rec = call("/v1/documents:deleteWhere", `{"collection":"c","filterMeta":{"kind":["log"]},"dryRun":true}`); rec.Code != 200 {
		t.Fatalf("read-only dry run: %d", rec.Code)
	}
	if rec = call("/v1/documents:deleteWhere", `{"collection":"c","filterMeta":{"kind":["log"]}}`); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only delete: %d", rec.Code)
	}
	g := &GRPCServer{server: s}
	ctx := context.Background()
	if _, err := g.DeleteWhere(ctx, &proto.WhereRequest{Collection: "c", FilterMeta: map[string]*proto.MetaValues{"kind": {Values: []string{"log"}}}}); err == nil {
		t.Fatal("gRPC write in read-only mode")
	}
	s.Mode = ModeRW

	// MCP.
	tools := &MCPToolServer{client: NewDirectClient(s)}
	out, err := tools.mcpCallTool(ctx, "update_where", map[string]interface{}{"collection": "c",
		"filter": map[string]interface{}{"kind": map[string]interface{}{"in": []interface{}{"page"}}}, "meta": map[string]interface{}{"y": []interface{}{"2"}}})
	if err != nil || !strings.Contains(out, `"affected": 3`) {
		t.Fatalf("MCP update: %v %s", err, out)
	}
	if out, err = tools.mcpCallTool(ctx, "set_ttl_where", map[string]interface{}{"collection": "c", "filter_meta": map[string]interface{}{"kind": []interface{}{"page"}}, "ttl": float64(60)}); err != nil || !strings.Contains(out, `"affected": 3`) {
		t.Fatalf("MCP ttl: %v %s", err, out)
	}
	if _, err = tools.mcpCallTool(ctx, "delete_where", map[string]interface{}{"collection": "c", "filter": "bad"}); err == nil {
		t.Fatal("MCP bad filter")
	}
	if _, err = tools.mcpCallTool(ctx, "delete_where", map[string]interface{}{"collection": "c"}); err == nil {
		t.Fatal("MCP no filter")
	}
	if out, err = tools.mcpCallTool(ctx, "delete_where", map[string]interface{}{"collection": "c", "filter_meta": map[string]interface{}{"kind": []interface{}{"log"}}, "dry_run": true}); err != nil || !strings.Contains(out, `"dry_run"`) && !strings.Contains(out, `"dryRun": true`) {
		t.Fatalf("MCP dry run: %v %s", err, out)
	}

	// gRPC.
	byKind := func(v string) map[string]*proto.MetaValues {
		return map[string]*proto.MetaValues{"kind": {Values: []string{v}}}
	}
	ttl := int64(0)
	if gr, err := g.SetTTLWhere(ctx, &proto.WhereRequest{Collection: "c", FilterMeta: byKind("page"), Ttl: &ttl}); err != nil || gr.Affected != 3 {
		t.Fatalf("gRPC ttl: %v %+v", err, gr)
	}
	if gr, err := g.UpdateWhere(ctx, &proto.WhereRequest{Collection: "c", FilterMeta: byKind("page"), Meta: byKind("page2"), DryRun: true}); err != nil || gr.Matched != 3 {
		t.Fatalf("gRPC update dry: %v %+v", err, gr)
	}
	gr, err := g.DeleteWhere(ctx, &proto.WhereRequest{Collection: "c", Filter: map[string]*proto.MetaCondition{"kind": {AnyOf: []string{"log"}}}, Limit: 2})
	if err != nil || gr.Affected != 2 || gr.NextCursor == "" {
		t.Fatalf("gRPC delete: %v %+v", err, gr)
	}
	if _, err := g.DeleteWhere(ctx, &proto.WhereRequest{Collection: "c"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("gRPC invalid: %v", err)
	}
	keys := whereKeys(t, s)
	sort.Strings(keys)
	if len(keys) != 5 {
		t.Fatalf("after gRPC delete with limit 2: %v", keys)
	}

	// Auth: an anonymous caller is refused on both.
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	am, amCleanup := authMwSetup(t)
	defer amCleanup()
	s.AuthManager = am
	if rec = call("/v1/documents:deleteWhere", `{"collection":"c","filterMeta":{"kind":["log"]},"dryRun":true}`); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous REST: %d", rec.Code)
	}
	if _, err := g.DeleteWhere(ctx, &proto.WhereRequest{Collection: "c", DryRun: true}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("anonymous gRPC: %v", err)
	}
}
