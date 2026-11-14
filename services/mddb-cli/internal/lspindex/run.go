package lspindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
)

// run is the state of one Index call. Phase one writes defs; phases two and
// three only read them and write refs and impls, under mu.
type run struct {
	spec   ServerSpec
	opt    Options
	root   string
	conn   *conn
	server string

	mu        sync.Mutex
	defs      map[string][]symbol                // file → its definitions
	refs      map[string]map[string]map[int]bool // file → target → lines
	impls     map[string]map[Impl]bool           // file → implementations
	refsAsked int                                // references requests sent
	refsLost  int                                // of those, failed or timed out
	reqErrors []string                           // distinct error answers, for the canary
	retry     []openFile                         // documentSymbol to ask again
	opened    map[string]bool                    // files sent with didOpen
	fallbacks *fallbacks                         // nil unless the spec reads stderr
	stats     Stats
}

// maxReqErrors bounds the distinct error answers kept.
const maxReqErrors = 5

func newRun(spec ServerSpec, opt Options, root string, c *conn) *run {
	return &run{spec: spec, opt: opt, root: root, conn: c,
		defs: map[string][]symbol{}, opened: map[string]bool{}, refs: map[string]map[string]map[int]bool{}, impls: map[string]map[Impl]bool{}}
}

// count updates the stats under the lock.
func (r *run) count(f func(*Stats)) {
	r.mu.Lock()
	f(&r.stats)
	r.mu.Unlock()
}

// request sends one request and gives its item up on a failure that costs
// this item only (see try). ok is false then; err is set for a failure that
// ends the run.
func (r *run) request(ctx context.Context, method string, params, result any) (ok bool, err error) {
	lost, err := r.try(ctx, method, params, result)
	if lost != nil {
		r.lose(method, lost)
	}
	return lost == nil && err == nil, err
}

// try sends one request bounded by RequestTimeout. lost is an error answer,
// a timeout or a result that does not decode; err is a failure that ends the
// run (the server is gone, ctx ended).
func (r *run) try(ctx context.Context, method string, params, result any) (lost, err error) {
	rctx, cancel := context.WithTimeout(ctx, r.opt.RequestTimeout)
	defer cancel()
	err = r.conn.call(rctx, method, params, result)
	switch {
	case err == nil:
		return nil, nil
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(err, errServerGone):
		return nil, err
	}
	return err, nil
}

// lose counts an item given up after a failed request.
func (r *run) lose(method string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats.Skipped++
	if method == methodReferences {
		r.refsLost++
	}
	if errors.Is(err, context.DeadlineExceeded) {
		r.stats.TimedOut++
	} else if msg := clipMessage(method + ": " + err.Error()); len(r.reqErrors) < maxReqErrors && !slices.Contains(r.reqErrors, msg) {
		r.reqErrors = append(r.reqErrors, msg)
	}
}

const methodReferences = "textDocument/references"

// openFile is one file sent with textDocument/didOpen.
type openFile struct {
	rel     string
	src     []byte
	retried bool // documentSymbol failed once already
}

// openAll sends didOpen for every readable file before any question is
// asked. pyrefly cancels a pending documentSymbol when another didOpen
// arrives ("canceled due to subsequent mutation"), so opening while asking
// would lose answers at any concurrency.
func (r *run) openAll(files []string) ([]openFile, error) {
	var out []openFile
	for _, rel := range files {
		abs := filepath.Join(r.root, filepath.FromSlash(rel))
		src, err := os.ReadFile(abs) // #nosec G304 -- a file the caller listed, joined under the checkout root
		if err != nil {
			r.stats.Skipped++
			continue
		}
		err = r.conn.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": fileURI(abs), "languageId": r.spec.languageID(rel), "version": 1, "text": string(src)}})
		if err != nil {
			return nil, err
		}
		r.opened[rel] = true
		out = append(out, openFile{rel: rel, src: src})
	}
	return out, nil
}

// symbolsOf records an open file's definitions. A failed request is tried
// once more after the first pass: right after a thousand didOpens gopls is
// busy checking them, and documentSymbol may time out on a file it answers
// in milliseconds a minute later.
func (r *run) symbolsOf(ctx context.Context, f openFile) error {
	const method = "textDocument/documentSymbol"
	uri := fileURI(filepath.Join(r.root, filepath.FromSlash(f.rel)))
	var nodes []documentSymbol
	lost, err := r.try(ctx, method, map[string]any{"textDocument": textDocumentID{URI: uri}}, &nodes)
	switch {
	case err != nil:
		return err
	case lost != nil && f.retried:
		r.lose(method, lost)
		return nil
	case lost != nil:
		f.retried = true
		r.mu.Lock()
		r.retry = append(r.retry, f)
		r.mu.Unlock()
		return nil
	}
	rel := f.rel
	syms, bad := r.spec.flatten(rel, f.src, nodes)
	r.mu.Lock()
	r.defs[rel] = syms
	r.stats.Unencodable += bad
	r.mu.Unlock()
	return nil
}

// symbolsWhere lists the definitions passing keep, in a fixed order.
func (r *run) symbolsWhere(keep func(*symbol) bool) []*symbol {
	var out []*symbol
	for _, path := range sortedKeys(r.defs) {
		for i := range r.defs[path] {
			if s := &r.defs[path][i]; keep(s) {
				out = append(out, s)
			}
		}
	}
	return out
}

func (r *run) wantsImpls(s *symbol) bool {
	return slices.Contains(r.spec.ImplKinds, s.lspKind)
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
