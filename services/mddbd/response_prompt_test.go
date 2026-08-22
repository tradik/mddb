package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/fts"
	proto "mddb/proto"
)

// RAG-002. The instruction for how to format an answer used to live in every
// client, so a runbook collection and an API-docs collection looked identical
// to all of them. These pin that it travels with the data instead.

func TestResponsePromptExpandsTemplateVariables(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	if err := srv.CollectionManager.Set("runbooks", &CollectionConfig{
		Type:           "default",
		ResponsePrompt: "Answer about {{collection}} as numbered steps. The question was: {{query}}",
	}); err != nil {
		t.Fatal(err)
	}

	got := srv.ResponsePrompt("runbooks", "how do I restart it?")
	if !strings.Contains(got, "about runbooks as") {
		t.Errorf("{{collection}} was not expanded: %s", got)
	}
	if !strings.Contains(got, "how do I restart it?") {
		t.Errorf("{{query}} was not expanded: %s", got)
	}
}

// A collection without a prompt must change nothing anywhere.
func TestResponsePromptIsEmptyWhenUnset(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	if err := srv.CollectionManager.Set("plain", &CollectionConfig{Type: "default"}); err != nil {
		t.Fatal(err)
	}

	for _, collection := range []string{"plain", "never-configured", ""} {
		if got := srv.ResponsePrompt(collection, "q"); got != "" {
			t.Errorf("collection %q returned %q, want empty", collection, got)
		}
	}
	if got := (*Server)(nil).ResponsePrompt("any", "q"); got != "" {
		t.Errorf("a nil server returned %q", got)
	}
	if got := (&Server{}).ResponsePrompt("any", "q"); got != "" {
		t.Errorf("a server without a collection manager returned %q", got)
	}
}

// The cap exists because this text is prepended to prompts automatically: an
// unbounded value silently eats the context the answer needs.
func TestResponsePromptValidation(t *testing.T) {
	if err := ValidateResponsePrompt(""); err != nil {
		t.Errorf("an empty prompt was rejected: %v", err)
	}
	if err := ValidateResponsePrompt(strings.Repeat("x", MaxResponsePromptBytes)); err != nil {
		t.Errorf("a prompt at exactly the limit was rejected: %v", err)
	}
	if err := ValidateResponsePrompt(strings.Repeat("x", MaxResponsePromptBytes+1)); err == nil {
		t.Error("an oversized prompt was accepted")
	}
	if err := ValidateResponsePrompt(string([]byte{0xff, 0xfe})); err == nil {
		t.Error("invalid UTF-8 was accepted")
	}
}

// --- REST ---

func TestRESTStoresAndValidatesTheResponsePrompt(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	body := `{"collection":"runbooks","type":"default","responsePrompt":"Answer as numbered steps."}`
	req := httptest.NewRequest("PUT", "/v1/collection-config", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleCollectionConfigSet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if got := srv.ResponsePrompt("runbooks", "q"); got != "Answer as numbered steps." {
		t.Errorf("stored prompt = %q", got)
	}

	oversized := `{"collection":"runbooks","responsePrompt":"` + strings.Repeat("x", MaxResponsePromptBytes+1) + `"}`
	req = httptest.NewRequest("PUT", "/v1/collection-config", strings.NewReader(oversized))
	w = httptest.NewRecorder()
	srv.handleCollectionConfigSet(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("an oversized prompt gave %d, want 400", w.Code)
	}
}

// --- gRPC ---

func TestGRPCRoundTripsTheResponsePrompt(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	g := &GRPCServer{server: srv}
	if _, err := g.SetCollectionConfig(context.Background(), &proto.SetCollectionConfigRequest{
		Collection:     "runbooks",
		Type:           "default",
		ResponsePrompt: "Answer as numbered steps.",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := g.GetCollectionConfig(context.Background(), &proto.GetCollectionConfigRequest{Collection: "runbooks"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config == nil || resp.Config.ResponsePrompt != "Answer as numbered steps." {
		t.Errorf("the prompt did not survive the round trip: %+v", resp.Config)
	}

	_, err = g.SetCollectionConfig(context.Background(), &proto.SetCollectionConfigRequest{
		Collection:     "runbooks",
		ResponsePrompt: strings.Repeat("x", MaxResponsePromptBytes+1),
	})
	if err == nil {
		t.Error("an oversized prompt was accepted over gRPC")
	}
}

// --- MCP ---

// The same defect as the gRPC path: MCP's set_collection_config rebuilt the
// stored struct from a partial view, so an agent updating a description left an
// encrypted collection writing plaintext.
func TestMCPSetConfigPreservesFieldsItCannotExpress(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	if err := srv.CollectionManager.Set("secure", &CollectionConfig{
		Type:            "default",
		StorageBackend:  "s3",
		Quantization:    "int8",
		DiskOnlyVectors: true,
		Encrypted:       true,
		SpellCorrect:    true,
		Retrieval:       &RetrievalProfileDef{TopK: 40},
		ResponsePrompt:  "Answer as numbered steps.",
	}); err != nil {
		t.Fatal(err)
	}

	if err := NewDirectClient(srv).SetCollectionConfig(context.Background(), &MCPSetCollectionConfigRequest{
		Collection:  "secure",
		Type:        "default",
		Description: "updated by an agent",
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := srv.CollectionManager.Get("secure")
	if got.Description != "updated by an agent" {
		t.Errorf("the update did not apply: %q", got.Description)
	}
	if !got.Encrypted {
		t.Error("an MCP description update disabled at-rest encryption")
	}
	if got.StorageBackend != "s3" || got.Quantization != "int8" || !got.DiskOnlyVectors || !got.SpellCorrect {
		t.Errorf("storage and vector settings were erased: %+v", got)
	}
	if got.Retrieval == nil || got.Retrieval.TopK != 40 {
		t.Errorf("the retrieval profile was erased: %+v", got.Retrieval)
	}
	if got.ResponsePrompt != "Answer as numbered steps." {
		t.Errorf("the response prompt was erased: %q", got.ResponsePrompt)
	}
}

func TestMCPSetConfigWritesAndValidatesThePrompt(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()
	client := NewDirectClient(srv)

	if err := client.SetCollectionConfig(context.Background(), &MCPSetCollectionConfigRequest{
		Collection:     "runbooks",
		Type:           "default",
		ResponsePrompt: "Answer as numbered steps.",
	}); err != nil {
		t.Fatal(err)
	}
	if got := srv.ResponsePrompt("runbooks", "q"); got != "Answer as numbered steps." {
		t.Errorf("stored prompt = %q", got)
	}

	if err := client.SetCollectionConfig(context.Background(), &MCPSetCollectionConfigRequest{
		Collection:     "runbooks",
		ResponsePrompt: strings.Repeat("x", MaxResponsePromptBytes+1),
	}); err == nil {
		t.Error("an oversized prompt was accepted over MCP")
	}

	if err := client.SetCollectionConfig(context.Background(), &MCPSetCollectionConfigRequest{
		Collection: "runbooks",
		Retrieval:  &RetrievalProfileDef{TopK: -1},
	}); err == nil {
		t.Error("an invalid retrieval profile was accepted over MCP")
	}
}

// An agent must get the instruction in the same round trip that fetched the
// results, not in a second call it may never make.
func TestMCPSearchResultsCarryTheResponsePrompt(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()
	srv.FTSIndex = fts.NewFTSIndex(srv.DB)
	if err := srv.FTSIndex.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}

	if err := srv.CollectionManager.Set("runbooks", &CollectionConfig{
		Type:           "default",
		ResponsePrompt: "Answer about {{collection}} as numbered steps.",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := NewDirectClient(srv).FTSSearch(context.Background(), &MCPFTSSearchRequest{
		Collection: "runbooks",
		Query:      "restart",
		Limit:      5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ResponsePrompt != "Answer about runbooks as numbered steps." {
		t.Errorf("the prompt did not reach the search results: %q", resp.ResponsePrompt)
	}
}

func TestMCPSearchResultsOmitAnUnsetPrompt(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()
	srv.FTSIndex = fts.NewFTSIndex(srv.DB)
	if err := srv.FTSIndex.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}

	resp, err := NewDirectClient(srv).FTSSearch(context.Background(), &MCPFTSSearchRequest{
		Collection: "plain",
		Query:      "anything",
		Limit:      5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ResponsePrompt != "" {
		t.Errorf("an unconfigured collection produced a prompt: %q", resp.ResponsePrompt)
	}
}
