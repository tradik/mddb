package main

import (
	"context"
	"net/http"
	"testing"

	pb "mddb/proto"
)

// #319: the code settings travel every transport, and setting them leaves the
// rest of the config alone.

func TestGRPCSetsCodeConfigWithoutTouchingTheRest(t *testing.T) {
	gs, _, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	if err := gs.server.CollectionManager.Set("k", &CollectionConfig{Type: "default", Quantization: "int8", SpellLang: "pl"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection: "k", Code: &pb.CodeConfigProto{IncludePaths: []string{"src"}},
	}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := gs.server.CollectionManager.Get("k")
	if cfg.Code == nil || len(cfg.Code.IncludePaths) != 1 || cfg.Code.IncludePaths[0] != "src" {
		t.Errorf("code = %+v", cfg.Code)
	}
	// The merged fields stay; type, description, icon, color, custom_meta,
	// max_revisions and response_prompt are set as sent, like REST PUT.
	if cfg.Quantization != "int8" || cfg.SpellLang != "pl" {
		t.Errorf("a merged field changed: quantization %q, spellLang %q", cfg.Quantization, cfg.SpellLang)
	}
	if codeConfigToProto(nil) != nil || codeConfigFromProto(nil) != nil {
		t.Error("nil code config should stay nil")
	}
}

func TestRESTSetsCodeConfig(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rec := doRequest(t, s.handleCollectionConfigSet, map[string]any{
		"collection": "myproject", "code": map[string]any{"includePaths": []string{"src"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := s.codeIncludePaths("myproject"); len(got) != 1 || got[0] != "src" {
		t.Errorf("include paths = %v", got)
	}
}

// Found while adding the field: set_collection_config advertised
// max_revisions but never read it, and the merge it documents assigned six
// fields unconditionally, so an agent changing the description erased the
// icon, the colour and the custom meta and reset maxRevisions to unlimited.
func TestMCPSetCollectionConfigChangesOnlyWhatWasSent(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.CollectionManager.Set("blog", &CollectionConfig{
		Type: "website", Icon: "📰", Color: "#123456", CustomMeta: map[string]string{"team": "a"}, MaxRevisions: 3,
	}); err != nil {
		t.Fatal(err)
	}
	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}

	if _, err := ts.toolSetCollectionConfig(context.Background(), map[string]interface{}{
		"collection": "blog", "description": "only this",
	}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.CollectionManager.Get("blog")
	if cfg.Description != "only this" || cfg.Type != "website" || cfg.Icon != "📰" || cfg.Color != "#123456" ||
		cfg.CustomMeta["team"] != "a" || cfg.MaxRevisions != 3 {
		t.Errorf("an unsent field changed: %+v", cfg)
	}

	if _, err := ts.toolSetCollectionConfig(context.Background(), map[string]interface{}{
		"collection": "blog", "max_revisions": float64(0), "code": map[string]interface{}{"include_paths": []interface{}{"src"}},
	}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.CollectionManager.Get("blog")
	if cfg.MaxRevisions != 0 {
		t.Errorf("max_revisions 0 (unlimited) was not applied: %d", cfg.MaxRevisions)
	}
	if cfg.Code == nil || cfg.Code.IncludePaths[0] != "src" {
		t.Errorf("code = %+v", cfg.Code)
	}

	neg := -1
	if err := NewDirectClient(s).SetCollectionConfig(context.Background(), &MCPSetCollectionConfigRequest{Collection: "blog", MaxRevisions: &neg}); err == nil {
		t.Error("a negative maxRevisions was accepted")
	}
}
