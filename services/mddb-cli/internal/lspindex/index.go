package lspindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Index starts spec's language server in root, asks it about files and
// returns the facts. files are paths relative to root (as autoindex lists
// them); a file outside root is skipped.
//
// The run opens every file, asks documentSymbol for each, waits for the
// server's loading progress to end, then asks references for every
// definition (bar spec.SkipRefs) and implementation for spec.ImplKinds, at
// most opt.Concurrency requests in flight. A request that fails or times out
// costs its symbol (Stats.Skipped), not the run. The run fails when the
// server cannot be found or started, dies, breaks the protocol, opt.Timeout
// passes, or the server never loaded the workspace (ErrWorkspaceNotLoaded);
// it then returns no facts. Errors from a started server are *ServerError,
// carrying the server's error messages and stderr.
//
// Two runs over the same tree and server return equal results.
func Index(ctx context.Context, root string, files []string, spec ServerSpec, opt Options) (*Result, error) {
	began := time.Now()
	opt = opt.withDefaults()
	if opt.Getenv == nil {
		opt.Getenv = os.Getenv
	}
	if opt.LookPath == nil {
		opt.LookPath = exec.LookPath
	}
	root, err := realRoot(root)
	if err != nil {
		return nil, err
	}
	cand, err := spec.resolve(opt.Getenv, opt.LookPath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	argv := spec.command(cand, opt)
	var fb *fallbacks
	var onLine func(string)
	if spec.Fallback != nil {
		fb = &fallbacks{read: spec.Fallback}
		onLine = fb.line
	}
	srv, err := startServer(ctx, root, argv, onLine)
	if err != nil {
		return nil, &ServerError{Server: strings.Join(argv, " "), Err: err}
	}
	r := newRun(spec, opt, root, srv.conn)
	r.fallbacks = fb
	err = r.execute(ctx, cand, files)
	r.stats.PeakRssMb = srv.peakRssMb() // VmHWM is gone once the server exits
	srv.stop()
	r.stats.Requests = int(srv.conn.requests.Load())
	if err == nil {
		err = r.fallbackError() // the server is gone: every stderr line has been read
	}
	if err != nil {
		if ctx.Err() != nil && !errors.Is(err, ErrWorkspaceNotLoaded) {
			err = fmt.Errorf("indexing %s stopped: %w", spec.Language, ctx.Err())
		}
		return nil, &ServerError{Server: strings.Join(argv, " "), Err: err,
			Messages: append(srv.conn.serverErrors(), r.reqErrors...), Stderr: srv.stderr.String()}
	}
	res := r.result()
	res.Stats.Elapsed = time.Since(began)
	return res, nil
}

// realRoot makes root absolute with symlinks resolved: servers report real
// paths, and a root seen through a symlink would put every location
// "outside" it.
func realRoot(root string) (string, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

// execute runs the phases in order. Each phase needs the previous one whole:
// references are asked per definition, implementations are attributed to
// the definitions of the implementing file.
func (r *run) execute(ctx context.Context, cand Candidate, files []string) error {
	if err := r.initialize(ctx, cand); err != nil {
		return err
	}
	opened, err := r.openAll(r.relFiles(files))
	if err != nil {
		return err
	}
	if err := forEach(ctx, r.opt.Concurrency, opened, r.symbolsOf); err != nil {
		return err
	}
	if err := forEach(ctx, r.opt.Concurrency, r.retry, r.symbolsOf); err != nil {
		return err
	}
	// Every file has been parsed: a fallback compile has been logged by now,
	// and references would be asked for nothing.
	if err := r.fallbackError(); err != nil {
		return err
	}
	waited := time.Now()
	limit := maxLoadWait
	if r.spec.WaitIndexed {
		limit = r.opt.Timeout
	}
	if err := r.conn.waitIdle(ctx, limit); err != nil {
		return err
	}
	r.stats.LoadWait = time.Since(waited)
	if err := forEach(ctx, r.opt.Concurrency, r.symbolsWhere(func(s *symbol) bool { return !s.skipRefs }), r.referencesOf); err != nil {
		return err
	}
	if err := r.canary(); err != nil {
		return err
	}
	return forEach(ctx, r.opt.Concurrency, r.symbolsWhere(r.wantsImpls), r.implementationsOf)
}

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
		"capabilities": map[string]any{
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
		},
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

// relFiles cleans the caller's paths to slash form and drops those outside
// the root or unusable as a document path.
func (r *run) relFiles(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		rel := filepath.ToSlash(filepath.Clean(f))
		switch {
		case filepath.IsAbs(f) || rel == ".." || strings.HasPrefix(rel, "../"):
			r.count(func(s *Stats) { s.Skipped++ })
		case !validPath(rel):
			r.count(func(s *Stats) { s.Unencodable++ })
		case !seen[rel]:
			seen[rel] = true
			out = append(out, rel)
		}
	}
	return out
}
