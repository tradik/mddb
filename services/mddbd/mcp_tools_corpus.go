package main

import (
	"context"
	"errors"
	"fmt"

	json "mddb/internal/jsonx"
)

// Corpus pack tools (AGT-005, #378): list, install from an https URL, remove.
//
// There is no corpus_build tool. A build's product is the pack file — a whole
// collection, up to MDDB_CORPUS_MAX_BYTES — and an MCP tool answers with text
// or JSON in the model's context: the pack would have to come back
// base64-encoded through the conversation, or be left on the server's disk
// for nobody to fetch. Building stays with the clients that can carry a file:
// POST /v1/corpus/build, mddb-cli corpus build and the BuildCorpusPack RPC.

// mcpCorpusTools are the corpus pack tools, in the admin toolset.
func mcpCorpusTools() []MCPTool {
	return []MCPTool{
		{
			Name: "corpus_list",
			Description: "List the corpus packs installed on this server: read-only collections of documentation built " +
				"elsewhere, each with its name, version, licence, document count and whether its vectors are searchable here.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name: "corpus_install",
			Description: "Install a corpus pack (.mddbpack) the server downloads from an https URL, as a read-only collection " +
				"named in the pack's manifest. Give sha256 to have the pack file checked before anything is installed. " +
				"Refused when a collection of that name already holds data. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"url":    map[string]interface{}{"type": "string", "description": "https:// URL of the .mddbpack file"},
				"sha256": map[string]interface{}{"type": "string", "description": "Expected sha256 of the pack file (hex)"},
			}, "required": []string{"url"}},
		},
		{
			Name: "corpus_remove",
			Description: "Remove an installed corpus pack: its collection, documents and indexes. Only a pack is removed " +
				"here; a collection written by hand is deleted with delete_collection. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"name": map[string]interface{}{"type": "string", "description": "The pack's collection name, as corpus_list shows it"},
			}, "required": []string{"name"}},
		},
	}
}

func (s *MCPToolServer) toolCorpusList(ctx context.Context, _ map[string]interface{}) (string, error) {
	packs, err := s.client.CorpusList(ctx)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(map[string]any{"packs": packs, "total": len(packs)}, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolCorpusInstall(ctx context.Context, args map[string]interface{}) (string, error) {
	url := mcpGetString(args, "url")
	if url == "" {
		return "", errors.New("url is required")
	}
	res, err := s.client.CorpusInstall(ctx, url, mcpGetString(args, "sha256"))
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(res, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolCorpusRemove(ctx context.Context, args map[string]interface{}) (string, error) {
	name := mcpGetString(args, "name")
	if name == "" {
		return "", errors.New("name is required")
	}
	n, err := s.client.CorpusRemove(ctx, name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"status":"removed","name":%q,"deleted":%d}`, name, n), nil
}
