package lspindex

import (
	"context"
	"fmt"
	"os"
)

// initialize performs the handshake and names the server for Result.Server.
func (r *run) initialize(ctx context.Context, cand Candidate) error {
	rootURI := fileURI(r.root)
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"rootPath":  r.root,
		"workspaceFolders": []workspaceFolder{
			{URI: rootURI, Name: "root"},
		},
		"capabilities": clientCapabilities(r.spec),
	}
	if r.spec.Settings != nil {
		if s := r.spec.Settings(r.opt); s != nil {
			params["initializationOptions"] = s
		}
	}
	var res struct {
		ServerInfo serverInfo `json:"serverInfo"`
	}
	if err := r.conn.call(ctx, "initialize", params, &res); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	r.server = serverName(cand, res.ServerInfo)
	return r.conn.notify("initialized", map[string]any{})
}

// clientCapabilities are what the client declares: progress, configuration
// and the three requests it asks, and for rust-analyzer the serverStatus
// notification it waits on (ServerSpec.ServerStatus).
func clientCapabilities(spec ServerSpec) map[string]any {
	caps := map[string]any{
		"window": map[string]any{"workDoneProgress": true},
		"workspace": map[string]any{
			"configuration":    true,
			"workspaceFolders": true,
			// clangd reads the kinds a client knows from here, for
			// documentSymbol too, and otherwise reports a struct as a
			// class and an enum's members as enums.
			"symbol": map[string]any{"symbolKind": allSymbolKinds},
		},
		"textDocument": map[string]any{
			"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true, "symbolKind": allSymbolKinds},
			"references":     map[string]any{},
			"implementation": map[string]any{},
		},
	}
	if spec.ServerStatus {
		caps["experimental"] = map[string]any{"serverStatusNotification": true}
	}
	return caps
}
