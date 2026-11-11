package main

import (
	"context"
	"errors"

	json "mddb/internal/jsonx"
)

// Module switch tools (GO-049), in the admin toolset: feature_list reads,
// feature_set needs admin. They call what REST and gRPC call, on the
// in-process server, as the bulk ingest tools do.

// mcpFeatureTools are the feature tools.
func mcpFeatureTools() []MCPTool {
	return []MCPTool{
		{
			Name: "feature_list",
			Description: "List the server's optional modules (chat, geo, temporal, spell, curation, automation, memory, " +
				"codeIntel, eval, corpus, classify, duplicates, images, webhooks, schemas, aliases, crossSearch, searchTuning, ftsTuning, chatExternalMcp) with whether each works now, its switch, " +
				"default, requirements, the environment lock and the store's version. A disabled module's tools are not listed.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name: "feature_set",
			Description: "Switch an optional module on or off. Its data stays; switching it back on restores it. A module " +
				"forced off by MDDB_FEATURES_FORCE_OFF cannot be switched. Turning chat off also stops chatExternalMcp. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"name":    map[string]interface{}{"type": "string", "description": "The module's name, e.g. geo"},
				"enabled": map[string]interface{}{"type": "boolean"},
			}, "required": []string{"name", "enabled"}},
		},
	}
}

// errFeatureToolsDirect: the tools need the in-process server.
var errFeatureToolsDirect = errors.New("feature tools require direct (in-process) MCP mode")

// featureServer returns the in-process server behind the tool server.
func (s *MCPToolServer) featureServer() (*Server, error) {
	dc, isDirect := s.client.(*DirectClient)
	if !isDirect || dc.server == nil {
		return nil, errFeatureToolsDirect
	}
	return dc.server, nil
}

func (s *MCPToolServer) toolFeatureList(_ context.Context, _ map[string]interface{}) (string, error) {
	srv, err := s.featureServer()
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(srv.FeaturesList(), "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolFeatureSet(ctx context.Context, args map[string]interface{}) (string, error) {
	srv, err := s.featureServer()
	if err != nil {
		return "", err
	}
	name := mcpGetString(args, "name")
	enabled, isBool := args["enabled"].(bool)
	if name == "" || !isBool {
		return "", errors.New("name and enabled (a boolean) are required")
	}
	st, err := srv.SetFeature(ctx, name, enabled)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(map[string]any{"feature": st, "version": srv.Features.Version()}, "", "  ")
	return string(data), nil
}
