package main

import (
	"context"
	"errors"
	"testing"
)

// mcpAdminFake answers the admin tools' client calls: with err set every
// call fails with it, otherwise each returns an empty success.
type mcpAdminFake struct {
	MCPClient
	err error
}

func (f *mcpAdminFake) DeleteCollection(context.Context, *MCPDeleteCollectionRequest) (*MCPDeleteCollectionResponse, error) {
	return &MCPDeleteCollectionResponse{}, f.err
}
func (f *mcpAdminFake) Truncate(context.Context, *MCPTruncateRequest) (*MCPTruncateResponse, error) {
	return &MCPTruncateResponse{}, f.err
}
func (f *mcpAdminFake) ListRevisions(context.Context, string, string, string) (*RevisionListResponse, error) {
	return &RevisionListResponse{}, f.err
}
func (f *mcpAdminFake) RestoreRevision(context.Context, string, string, string, int64) (*MCPDocument, error) {
	return &MCPDocument{}, f.err
}
func (f *mcpAdminFake) ListSynonyms(context.Context, string) (*MCPSynonymListResponse, error) {
	return &MCPSynonymListResponse{}, f.err
}
func (f *mcpAdminFake) SetSynonym(context.Context, string, string, []string) error { return f.err }
func (f *mcpAdminFake) DeleteSynonym(context.Context, string, string) error        { return f.err }
func (f *mcpAdminFake) ListStopWords(context.Context, string) (*MCPStopWordListResponse, error) {
	return &MCPStopWordListResponse{}, f.err
}
func (f *mcpAdminFake) AddStopWords(context.Context, string, []string) error    { return f.err }
func (f *mcpAdminFake) DeleteStopWords(context.Context, string, []string) error { return f.err }
func (f *mcpAdminFake) GetMetaKeys(context.Context, string) (*MCPMetaKeysResponse, error) {
	return &MCPMetaKeysResponse{}, f.err
}
func (f *mcpAdminFake) GetChecksum(context.Context, string) (*MCPChecksumResponse, error) {
	return &MCPChecksumResponse{}, f.err
}
func (f *mcpAdminFake) ListAutomation(context.Context, string) (*MCPAutomationListResponse, error) {
	return &MCPAutomationListResponse{}, f.err
}
func (f *mcpAdminFake) CreateAutomation(_ context.Context, r AutomationRule) (*AutomationRule, error) {
	return &r, f.err
}
func (f *mcpAdminFake) GetAutomation(context.Context, string) (*AutomationRule, error) {
	return &AutomationRule{}, f.err
}
func (f *mcpAdminFake) UpdateAutomation(_ context.Context, _ string, r AutomationRule) (*AutomationRule, error) {
	return &r, f.err
}
func (f *mcpAdminFake) DeleteAutomation(context.Context, string) error { return f.err }
func (f *mcpAdminFake) TestAutomation(context.Context, string) (string, error) {
	return "{}", f.err
}
func (f *mcpAdminFake) ListAutomationLogs(context.Context, int, string, string, string) (*MCPAutomationLogListResponse, error) {
	return &MCPAutomationLogListResponse{}, f.err
}

// mcpAdminToolArgs holds, per admin tool, arguments that pass the tool's own
// checks, so the call reaches the client.
var mcpAdminToolArgs = map[string]map[string]interface{}{
	"delete_collection":  {"collection": "c", "confirm": true},
	"truncate_revisions": {"collection": "c", "keep_revs": 1.0, "drop_cache": true, "confirm": true},
	"list_revisions":     {"collection": "c", "key": "k", "lang": "en"},
	"restore_revision":   {"collection": "c", "key": "k", "lang": "en", "timestamp": 1.0},
	"list_synonyms":      {"collection": "c"},
	"add_synonym":        {"collection": "c", "term": "t", "synonyms": []interface{}{"s", 1.0}},
	"delete_synonym":     {"collection": "c", "term": "t"},
	"list_stopwords":     {"collection": "c"},
	"add_stopwords":      {"collection": "c", "words": []interface{}{"w", 1.0}},
	"delete_stopwords":   {"collection": "c", "words": []interface{}{"w", 1.0}},
	"get_meta_keys":      {"collection": "c"},
	"get_checksum":       {"collection": "c"},
	"list_automation":    {"type": "cron"},
	"create_automation": {"type": "trigger", "name": "n", "enabled": false, "threshold": 5.0,
		"events": []interface{}{"insert", 1.0}, "headers": map[string]interface{}{"X": "y", "N": 1.0}},
	"get_automation":      {"id": "a"},
	"update_automation":   {"id": "a", "name": "n"},
	"delete_automation":   {"id": "a", "confirm": true},
	"test_automation":     {"id": "a"},
	"get_automation_logs": {"limit": 5.0, "rule_id": "a"},
}

// Every admin tool: refused without its arguments (where it takes any),
// reports a client failure, and answers on success.
func TestMCPAdminToolsCoverage(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	for name, args := range mcpAdminToolArgs {
		ts := &MCPToolServer{client: &mcpAdminFake{}, globalMode: ModeRW}
		if _, err := ts.mcpCallTool(ctx, name, args); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		ts.client = &mcpAdminFake{err: boom}
		if _, err := ts.mcpCallTool(ctx, name, args); !errors.Is(err, boom) {
			t.Errorf("%s with a failing client: %v", name, err)
		}
	}
	// The list arguments must not be empty.
	for _, tc := range []struct{ name, field string }{{"add_synonym", "synonyms"}, {"add_stopwords", "words"}, {"delete_stopwords", "words"}} {
		args := map[string]interface{}{"collection": "c", "term": "t", tc.field: []interface{}{}}
		ts := &MCPToolServer{client: &mcpAdminFake{}, globalMode: ModeRW}
		if _, err := ts.mcpCallTool(ctx, tc.name, args); err == nil {
			t.Errorf("%s with no %s succeeded", tc.name, tc.field)
		}
	}
	for _, name := range []string{"delete_collection", "list_revisions", "restore_revision", "list_synonyms", "add_synonym",
		"delete_synonym", "list_stopwords", "add_stopwords", "delete_stopwords", "get_meta_keys", "get_checksum",
		"get_automation", "update_automation", "delete_automation", "test_automation"} {
		ts := &MCPToolServer{client: &mcpAdminFake{}, globalMode: ModeRW}
		if _, err := ts.mcpCallTool(ctx, name, map[string]interface{}{"confirm": true}); err == nil {
			t.Errorf("%s without its arguments succeeded", name)
		}
	}
}
