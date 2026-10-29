package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	json "mddb/internal/jsonx"
)

// errCovBClient is the sentinel every covBFakeClient method returns when its err
// field is set, so a test can tell the tool passed the client's error through
// rather than inventing one of its own.
var errCovBClient = errors.New("covB: client failure")

// covBFakeClient is an MCPClient that records the last request of each call
// and answers with canned responses, or with err when it is set. Methods the
// MCP tools under test never call are left to the embedded nil interface and
// panic if reached, which would itself be a test failure.
type covBFakeClient struct {
	MCPClient

	mu    sync.Mutex
	err   error
	calls map[string]interface{}

	addDoc     *MCPDocument
	searchResp *MCPSearchResponse
	vecResp    *MCPVectorSearchResponse
	ftsResp    *MCPFTSSearchResponse
	hybridResp *MCPHybridSearchResponse
	crossResp  *MCPCrossSearchResponse
	// addErrAfter makes Add fail from the n-th call onwards (1-based); 0 = never.
	addErrAfter int
	addCount    int
}

func covBNewFake() *covBFakeClient {
	return &covBFakeClient{calls: map[string]interface{}{}}
}

// covBRecord stores the request for a method and reports the injected error.
func (f *covBFakeClient) covBRecord(name string, req interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[name] = req
	return f.err
}

// covBCall returns the request recorded for a method, failing the test if
// the method was never called.
func (f *covBFakeClient) covBCall(t *testing.T, name string) interface{} {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	req, ok := f.calls[name]
	if !ok {
		t.Fatalf("client method %s was never called", name)
	}
	return req
}

func (f *covBFakeClient) Stats(_ context.Context) (*MCPStats, error) {
	if err := f.covBRecord("Stats", nil); err != nil {
		return nil, err
	}
	return &MCPStats{}, nil
}

func (f *covBFakeClient) Add(_ context.Context, req *MCPAddRequest) (*MCPDocument, error) {
	if err := f.covBRecord("Add", req); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.addCount++
	n := f.addCount
	f.mu.Unlock()
	if f.addErrAfter > 0 && n >= f.addErrAfter {
		return nil, errCovBClient
	}
	if f.addDoc != nil {
		return f.addDoc, nil
	}
	return &MCPDocument{ID: "doc-" + req.Key, Key: req.Key, Lang: req.Lang, Meta: req.Meta, ContentMD: req.ContentMD}, nil
}

func (f *covBFakeClient) AddBatch(_ context.Context, req *MCPAddBatchRequest) (*MCPAddBatchResponse, error) {
	if err := f.covBRecord("AddBatch", req); err != nil {
		return nil, err
	}
	return &MCPAddBatchResponse{}, nil
}

func (f *covBFakeClient) DeleteBatch(_ context.Context, req *MCPDeleteBatchRequest) (*MCPDeleteBatchResponse, error) {
	if err := f.covBRecord("DeleteBatch", req); err != nil {
		return nil, err
	}
	return &MCPDeleteBatchResponse{}, nil
}

func (f *covBFakeClient) Search(_ context.Context, req *MCPSearchRequest) (*MCPSearchResponse, error) {
	if err := f.covBRecord("Search", req); err != nil {
		return nil, err
	}
	if f.searchResp != nil {
		return f.searchResp, nil
	}
	return &MCPSearchResponse{}, nil
}

func (f *covBFakeClient) Delete(_ context.Context, req *MCPDeleteRequest) error {
	return f.covBRecord("Delete", req)
}

func (f *covBFakeClient) Export(_ context.Context, req *MCPExportRequest) (io.ReadCloser, error) {
	if err := f.covBRecord("Export", req); err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *covBFakeClient) Backup(_ context.Context, req *MCPBackupRequest) (*MCPBackupResponse, error) {
	if err := f.covBRecord("Backup", req); err != nil {
		return nil, err
	}
	return &MCPBackupResponse{Backup: req.To}, nil
}

func (f *covBFakeClient) Restore(_ context.Context, req *MCPRestoreRequest) (*MCPRestoreResponse, error) {
	if err := f.covBRecord("Restore", req); err != nil {
		return nil, err
	}
	return &MCPRestoreResponse{Restored: req.From}, nil
}

func (f *covBFakeClient) VectorSearch(_ context.Context, req *MCPVectorSearchRequest) (*MCPVectorSearchResponse, error) {
	if err := f.covBRecord("VectorSearch", req); err != nil {
		return nil, err
	}
	if f.vecResp != nil {
		return f.vecResp, nil
	}
	return &MCPVectorSearchResponse{}, nil
}

func (f *covBFakeClient) VectorReindex(_ context.Context, req *MCPVectorReindexRequest) (*MCPVectorReindexResponse, error) {
	if err := f.covBRecord("VectorReindex", req); err != nil {
		return nil, err
	}
	return &MCPVectorReindexResponse{}, nil
}

func (f *covBFakeClient) VectorStats(_ context.Context) (*MCPVectorStatsResponse, error) {
	if err := f.covBRecord("VectorStats", nil); err != nil {
		return nil, err
	}
	return &MCPVectorStatsResponse{}, nil
}

func (f *covBFakeClient) ImportURL(_ context.Context, req *MCPImportURLRequest) (*MCPDocument, error) {
	if err := f.covBRecord("ImportURL", req); err != nil {
		return nil, err
	}
	return &MCPDocument{Key: req.Key}, nil
}

func (f *covBFakeClient) SetTTL(_ context.Context, req *MCPSetTTLRequest) (*MCPDocument, error) {
	if err := f.covBRecord("SetTTL", req); err != nil {
		return nil, err
	}
	return &MCPDocument{Key: req.Key}, nil
}

func (f *covBFakeClient) FTSSearch(_ context.Context, req *MCPFTSSearchRequest) (*MCPFTSSearchResponse, error) {
	if err := f.covBRecord("FTSSearch", req); err != nil {
		return nil, err
	}
	if f.ftsResp != nil {
		return f.ftsResp, nil
	}
	return &MCPFTSSearchResponse{}, nil
}

func (f *covBFakeClient) FTSReindex(_ context.Context, req *MCPFTSReindexRequest) (*MCPFTSReindexResponse, error) {
	if err := f.covBRecord("FTSReindex", req); err != nil {
		return nil, err
	}
	return &MCPFTSReindexResponse{}, nil
}

func (f *covBFakeClient) FTSLanguages(_ context.Context) (*MCPFTSLanguagesResponse, error) {
	if err := f.covBRecord("FTSLanguages", nil); err != nil {
		return nil, err
	}
	return &MCPFTSLanguagesResponse{}, nil
}

func (f *covBFakeClient) HybridSearch(_ context.Context, req *MCPHybridSearchRequest) (*MCPHybridSearchResponse, error) {
	if err := f.covBRecord("HybridSearch", req); err != nil {
		return nil, err
	}
	if f.hybridResp != nil {
		return f.hybridResp, nil
	}
	return &MCPHybridSearchResponse{}, nil
}

func (f *covBFakeClient) RegisterWebhook(_ context.Context, req *MCPRegisterWebhookRequest) (*MCPWebhook, error) {
	if err := f.covBRecord("RegisterWebhook", req); err != nil {
		return nil, err
	}
	return &MCPWebhook{URL: req.URL}, nil
}

func (f *covBFakeClient) ListWebhooks(_ context.Context) ([]MCPWebhook, error) {
	if err := f.covBRecord("ListWebhooks", nil); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *covBFakeClient) DeleteWebhook(_ context.Context, req *MCPDeleteWebhookRequest) error {
	return f.covBRecord("DeleteWebhook", req)
}

func (f *covBFakeClient) SetSchema(_ context.Context, req *MCPSetSchemaRequest) error {
	return f.covBRecord("SetSchema", req)
}

func (f *covBFakeClient) GetSchema(_ context.Context, collection string) (*MCPSchemaResponse, error) {
	if err := f.covBRecord("GetSchema", collection); err != nil {
		return nil, err
	}
	return &MCPSchemaResponse{}, nil
}

func (f *covBFakeClient) DeleteSchema(_ context.Context, collection string) error {
	return f.covBRecord("DeleteSchema", collection)
}

func (f *covBFakeClient) ListSchemas(_ context.Context) (*MCPListSchemasResponse, error) {
	if err := f.covBRecord("ListSchemas", nil); err != nil {
		return nil, err
	}
	return &MCPListSchemasResponse{}, nil
}

func (f *covBFakeClient) ValidateDocument(_ context.Context, req *MCPValidateRequest) (*MCPValidateResponse, error) {
	if err := f.covBRecord("ValidateDocument", req); err != nil {
		return nil, err
	}
	return &MCPValidateResponse{}, nil
}

func (f *covBFakeClient) UpdateDocument(_ context.Context, req *MCPUpdateDocumentRequest) (*MCPDocument, error) {
	if err := f.covBRecord("UpdateDocument", req); err != nil {
		return nil, err
	}
	return &MCPDocument{Key: req.Key}, nil
}

func (f *covBFakeClient) GetDocumentMeta(_ context.Context, req *MCPGetDocMetaRequest) (*MCPDocMetaResponse, error) {
	if err := f.covBRecord("GetDocumentMeta", req); err != nil {
		return nil, err
	}
	return &MCPDocMetaResponse{}, nil
}

func (f *covBFakeClient) CodeGraph(_ context.Context, req *MCPCodeGraphRequest) (*GraphResult, error) {
	if err := f.covBRecord("CodeGraph", req); err != nil {
		return nil, err
	}
	return &GraphResult{}, nil
}

func (f *covBFakeClient) SearchAdvisor(_ context.Context, collection string) (*SearchRecommendation, error) {
	if err := f.covBRecord("SearchAdvisor", collection); err != nil {
		return nil, err
	}
	return &SearchRecommendation{}, nil
}

func (f *covBFakeClient) Classify(_ context.Context, req *MCPClassifyRequest) (*MCPClassifyResponse, error) {
	if err := f.covBRecord("Classify", req); err != nil {
		return nil, err
	}
	return &MCPClassifyResponse{}, nil
}

func (f *covBFakeClient) GetCollectionConfig(_ context.Context, collection string) (*MCPCollectionConfigResponse, error) {
	if err := f.covBRecord("GetCollectionConfig", collection); err != nil {
		return nil, err
	}
	return &MCPCollectionConfigResponse{}, nil
}

func (f *covBFakeClient) SetCollectionConfig(_ context.Context, req *MCPSetCollectionConfigRequest) error {
	return f.covBRecord("SetCollectionConfig", req)
}

func (f *covBFakeClient) ListCollectionConfigs(_ context.Context) (*MCPCollectionConfigListResponse, error) {
	if err := f.covBRecord("ListCollectionConfigs", nil); err != nil {
		return nil, err
	}
	return &MCPCollectionConfigListResponse{}, nil
}

func (f *covBFakeClient) CrossSearch(_ context.Context, req *MCPCrossSearchRequest) (*MCPCrossSearchResponse, error) {
	if err := f.covBRecord("CrossSearch", req); err != nil {
		return nil, err
	}
	if f.crossResp != nil {
		return f.crossResp, nil
	}
	return &MCPCrossSearchResponse{}, nil
}

func (f *covBFakeClient) IndexingStatus(_ context.Context, collection string) (*IndexingStatus, error) {
	if err := f.covBRecord("IndexingStatus", collection); err != nil {
		return nil, err
	}
	return &IndexingStatus{Collection: collection, PendingEmbeddings: 3}, nil
}

func (f *covBFakeClient) CodeSymbol(_ context.Context, req *SymbolRequest) (*SymbolResult, error) {
	if err := f.covBRecord("CodeSymbol", req); err != nil {
		return nil, err
	}
	return &SymbolResult{Collection: req.Collection, Symbol: req.Name}, nil
}

func (f *covBFakeClient) FindDuplicates(_ context.Context, req *MCPFindDuplicatesRequest) (*MCPFindDuplicatesResponse, error) {
	if err := f.covBRecord("FindDuplicates", req); err != nil {
		return nil, err
	}
	return &MCPFindDuplicatesResponse{}, nil
}

func (f *covBFakeClient) Ingest(_ context.Context, req *MCPIngestRequest) (*MCPIngestResponse, error) {
	if err := f.covBRecord("Ingest", req); err != nil {
		return nil, err
	}
	return &MCPIngestResponse{}, nil
}

func (f *covBFakeClient) Aggregate(_ context.Context, req *AggregateRequest) (*AggregateResponse, error) {
	if err := f.covBRecord("Aggregate", req); err != nil {
		return nil, err
	}
	return &AggregateResponse{}, nil
}

// covBUnmarshal decodes a tool's JSON output (optionally after a text
// prefix ending in a newline) into a generic map.
func covBUnmarshal(t *testing.T, out string) map[string]interface{} {
	t.Helper()
	if i := strings.Index(out, "{"); i > 0 {
		out = out[i:]
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return m
}

// covBAllErrClient extends covBFakeClient with every remaining MCPClient
// method, all failing with errCovBClient, so any built-in tool can be dispatched to
// it without reaching real storage, the network or a nil interface.
type covBAllErrClient struct {
	*covBFakeClient
}

func covBNewAllErr() *covBAllErrClient {
	f := covBNewFake()
	f.err = errCovBClient
	return &covBAllErrClient{covBFakeClient: f}
}

func (c *covBAllErrClient) Health(context.Context) (*MCPHealth, error) { return nil, errCovBClient }
func (c *covBAllErrClient) UpdateBatch(context.Context, *MCPUpdateBatchRequest) (*MCPUpdateBatchResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) Get(context.Context, *MCPGetRequest) (*MCPDocument, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) DeleteCollection(context.Context, *MCPDeleteCollectionRequest) (*MCPDeleteCollectionResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) Truncate(context.Context, *MCPTruncateRequest) (*MCPTruncateResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) GeoSearch(context.Context, *MCPGeoSearchRequest) (*MCPGeoSearchResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) GeoWithin(context.Context, *MCPGeoWithinRequest) (*MCPGeoSearchResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) GeoStats(context.Context) (*MCPGeoStatsResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) GeoEncode(context.Context, float64, float64, int) (string, error) {
	return "", errCovBClient
}
func (c *covBAllErrClient) GeoDecode(context.Context, string) (float64, float64, error) {
	return 0, 0, errCovBClient
}
func (c *covBAllErrClient) ListSynonyms(context.Context, string) (*MCPSynonymListResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) SetSynonym(context.Context, string, string, []string) error {
	return errCovBClient
}
func (c *covBAllErrClient) DeleteSynonym(context.Context, string, string) error { return errCovBClient }
func (c *covBAllErrClient) ListStopWords(context.Context, string) (*MCPStopWordListResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) AddStopWords(context.Context, string, []string) error {
	return errCovBClient
}
func (c *covBAllErrClient) DeleteStopWords(context.Context, string, []string) error {
	return errCovBClient
}
func (c *covBAllErrClient) GetMetaKeys(context.Context, string) (*MCPMetaKeysResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) GetChecksum(context.Context, string) (*MCPChecksumResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) ListAutomation(context.Context, string) (*MCPAutomationListResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) CreateAutomation(context.Context, AutomationRule) (*AutomationRule, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) GetAutomation(context.Context, string) (*AutomationRule, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) UpdateAutomation(context.Context, string, AutomationRule) (*AutomationRule, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) DeleteAutomation(context.Context, string) error { return errCovBClient }
func (c *covBAllErrClient) TestAutomation(context.Context, string) (string, error) {
	return "", errCovBClient
}
func (c *covBAllErrClient) ListAutomationLogs(context.Context, int, string, string, string) (*MCPAutomationLogListResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) ListRevisions(context.Context, string, string, string) (*RevisionListResponse, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) RestoreRevision(context.Context, string, string, string, int64) (*MCPDocument, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) ListCurationRules(context.Context, string) ([]*CurationRule, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) CreateCurationRule(context.Context, *CurationRule) (*CurationRule, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) UpdateCurationRule(context.Context, *CurationRule) (*CurationRule, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) DeleteCurationRule(context.Context, string) error { return errCovBClient }
func (c *covBAllErrClient) Close() error                                     { return nil }
func (c *covBAllErrClient) ExpandTargets(_ context.Context, targets []string) []string {
	return targets
}
func (c *covBAllErrClient) AddEvalQuery(context.Context, string, EvalQuery) (EvalQuery, error) {
	return EvalQuery{}, errCovBClient
}
func (c *covBAllErrClient) RunEval(context.Context, EvalRequest) (*EvalResult, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) Gain(context.Context, string, bool) (*GainReport, error) {
	return nil, errCovBClient
}

// TestCovBDispatchReachesEveryBuiltinTool sends every advertised built-in
// tool through mcpCallTool. None may fall through to "unknown tool": that
// would mean a tool is listed to clients but has no handler.
func TestCovBDispatchReachesEveryBuiltinTool(t *testing.T) {
	ts := &MCPToolServer{client: covBNewAllErr(), globalMode: ModeRW}
	tools := mcpBuiltinTools()
	if len(tools) == 0 {
		t.Fatal("no built-in tools")
	}
	for _, tool := range tools {
		_, err := ts.mcpCallTool(context.Background(), tool.Name, map[string]interface{}{})
		if err != nil && strings.Contains(err.Error(), "unknown tool") {
			t.Errorf("built-in tool %q has no handler: %v", tool.Name, err)
		}
	}
}

func TestCovBDispatchUnknownTool(t *testing.T) {
	ts := &MCPToolServer{client: covBNewFake(), globalMode: ModeRW}
	_, err := ts.mcpCallTool(context.Background(), "no_such_tool", nil)
	if err == nil || err.Error() != "unknown tool: no_such_tool" {
		t.Fatalf("err = %v, want unknown tool", err)
	}
}

// TestCovBDispatchClientErrorPassesThrough checks the dispatcher returns the
// handler's error unchanged for a tool whose arguments are valid.
func TestCovBDispatchClientErrorPassesThrough(t *testing.T) {
	ts := &MCPToolServer{client: covBNewAllErr(), globalMode: ModeRW}
	_, err := ts.mcpCallTool(context.Background(), "get_stats", nil)
	if !errors.Is(err, errCovBClient) {
		t.Fatalf("err = %v, want errCovBClient", err)
	}
}

// TestCovBReadOnlyModeRefusesWritesAllowsReads checks both the global and the
// per-protocol read-only switch: a write tool is refused before the client
// is touched, a read tool goes through.
func TestCovBReadOnlyModeRefusesWritesAllowsReads(t *testing.T) {
	cases := []struct {
		name string
		ts   *MCPToolServer
	}{
		{"global", &MCPToolServer{globalMode: ModeRead}},
		{"protocol override", &MCPToolServer{globalMode: ModeRW, mode: ModeRead}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := covBNewFake()
			tc.ts.client = fake
			for _, w := range []string{"add_document", "memory_start_session", "upload_file", "set_collection_config", "delete_document"} {
				_, err := tc.ts.mcpCallTool(context.Background(), w, map[string]interface{}{"user_id": "u", "collection": "c"})
				if err == nil || !strings.Contains(err.Error(), "not available in read-only mode") {
					t.Errorf("%s: err = %v, want read-only refusal", w, err)
				}
			}
			if len(fake.calls) != 0 {
				t.Errorf("a refused write reached the client: %v", fake.calls)
			}
			if _, err := tc.ts.mcpCallTool(context.Background(), "get_stats", nil); err != nil {
				t.Errorf("read tool refused in read-only mode: %v", err)
			}
			fake.covBCall(t, "Stats")
		})
	}
}

func TestCovBArgHelpers(t *testing.T) {
	m := map[string]interface{}{
		"s": "str", "f": 2.5, "i": 7, "b": "yes", "n": 0.0,
		"slice":  []interface{}{"a", 1.0, "b"},
		"nostr":  []interface{}{1.0},
		"native": []string{"x"},
		"one":    "solo",
		"empty":  "",
		"meta":   map[string]interface{}{"k": "v", "arr": []interface{}{"x", 2.0}, "bad": 3.0},
		"fmap":   map[string]interface{}{"title": 2.0, "skip": "x"},
		"fbad":   map[string]interface{}{"skip": "x"},
	}
	if mcpGetString(m, "s") != "str" || mcpGetString(m, "f") != "" {
		t.Error("mcpGetString")
	}
	if mcpGetInt(m, "f") != 2 || mcpGetInt(m, "i") != 7 || mcpGetInt(m, "s") != 0 {
		t.Error("mcpGetInt")
	}
	if mcpGetFloat(m, "f") != 2.5 || mcpGetFloat(m, "s") != 0 {
		t.Error("mcpGetFloat")
	}
	if !mcpGetBool(m, "b") || mcpGetBool(m, "n") || mcpGetBool(m, "s") {
		t.Error("mcpGetBool")
	}
	if v, ok := mcpCoerceBool("No"); v || !ok {
		t.Error("mcpCoerceBool no")
	}
	if _, ok := mcpCoerceBool(nil); ok {
		t.Error("mcpCoerceBool nil")
	}
	if got := mcpGetStringSlice(m, "slice"); len(got) != 2 || got[1] != "b" {
		t.Errorf("slice = %v", got)
	}
	if mcpGetStringSlice(m, "nostr") != nil || mcpGetStringSlice(m, "empty") != nil || mcpGetStringSlice(m, "f") != nil {
		t.Error("mcpGetStringSlice nil cases")
	}
	if got := mcpGetStringSlice(m, "native"); len(got) != 1 || got[0] != "x" {
		t.Errorf("native = %v", got)
	}
	if got := mcpGetStringSlice(m, "one"); len(got) != 1 || got[0] != "solo" {
		t.Errorf("one = %v", got)
	}
	meta := mcpGetMetaMap(m, "meta")
	if meta["k"][0] != "v" || len(meta["arr"]) != 1 || meta["arr"][0] != "x" {
		t.Errorf("meta = %v", meta)
	}
	if _, ok := meta["bad"]; ok {
		t.Error("non-string meta value kept")
	}
	if fm := mcpGetFloat64Map(m, "fmap"); len(fm) != 1 || fm["title"] != 2 {
		t.Errorf("fmap = %v", fm)
	}
	if mcpGetFloat64Map(m, "fbad") != nil || mcpGetFloat64Map(m, "missing") != nil {
		t.Error("mcpGetFloat64Map nil cases")
	}
}

func TestCovBFilterGroupByIndexWaitHelpers(t *testing.T) {
	if f, err := mcpGetFilter(map[string]interface{}{}); f != nil || err != nil {
		t.Errorf("absent filter = %v, %v", f, err)
	}
	if f, err := mcpGetFilter(map[string]interface{}{"filter": nil}); f != nil || err != nil {
		t.Errorf("null filter = %v, %v", f, err)
	}
	if _, err := mcpGetFilter(map[string]interface{}{"filter": make(chan int)}); err == nil || !strings.HasPrefix(err.Error(), "filter: ") {
		t.Errorf("unencodable filter: err = %v", err)
	}
	f, err := mcpGetFilter(map[string]interface{}{"filter": map[string]interface{}{"tag": map[string]interface{}{"in": []interface{}{"a"}, "exists": true}}})
	if err != nil || len(f["tag"].In) != 1 || f["tag"].Exists == nil || !*f["tag"].Exists {
		t.Errorf("filter = %+v, %v", f, err)
	}

	if g, err := mcpGetGroupBy(map[string]interface{}{"group_by": "path"}); g != nil || err != nil {
		t.Errorf("non-object group_by = %v, %v", g, err)
	}
	if g, err := mcpGetGroupBy(map[string]interface{}{"group_by": map[string]interface{}{"key": "path", "per_group": 3.0, "perGroup": 9.0}}); err != nil || g.PerGroup != 3 {
		t.Errorf("per_group wins: %+v, %v", g, err)
	}
	if _, err := mcpGetGroupBy(map[string]interface{}{"group_by": map[string]interface{}{"key": "path", "per_group": -1.0}}); err == nil {
		t.Error("negative per_group accepted")
	}

	w := mcpGetIndexWait(map[string]interface{}{"read_level": "indexed", "wait_for_index": 10.0, "wait_for_keys": []interface{}{"a", 1.0, "b"}})
	if w.ReadLevel != "indexed" || w.WaitForIndex != 10 || len(w.WaitForKeys) != 2 || w.WaitForKeys[1] != "b" {
		t.Errorf("index wait = %+v", w)
	}
	if w := mcpGetIndexWait(nil); w.ReadLevel != "" || w.WaitForKeys != nil {
		t.Errorf("empty index wait = %+v", w)
	}
}
