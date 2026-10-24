package main

import (
	"context"
	"testing"

	proto "mddb/proto"
)

// #319: a C project, one document per file, keyed from the repository root.
// No meta is sent: the extension makes it code. request.c is the example from
// the blog post, so the post shows what this test checks.

func loadCProject(t *testing.T, srv *Server, includePaths []string) {
	t.Helper()
	if includePaths != nil {
		if err := srv.CollectionManager.Set("myproject", &CollectionConfig{
			Type: "default", Code: &CodeConfigDef{IncludePaths: includePaths},
		}); err != nil {
			t.Fatal(err)
		}
	}
	bp := NewBatchProcessor(srv, 2)
	if _, err := bp.ProcessBatch(context.Background(), "myproject", []*proto.BatchDocument{
		makeBatchDoc("src/server/request.c", "en", `
#include <net/conn.h>
#include <util/buf.h>
#include "request_internal.h"

static int parse_header(struct conn *c, struct buf *b);

int
parse_request(struct conn *c)
{
	struct buf *b = buf_alloc(c->rx_len);

	if (parse_header(c, b) != 0)
		return (conn_close(c, ERR_BAD_REQUEST));
	return (dispatch(c, b));
}
`, nil, false),
		makeBatchDoc("src/util/buf.c", "en", `
#include <util/buf.h>
struct buf *
buf_alloc(size_t n)
{
	return (memcpy_fast(pool_get(), n));
}
`, nil, false),
		makeBatchDoc("src/net/conn.h", "en", "struct conn { int rx_len; };\n#define CONN_LOCK(c) lock_acquire(c)\n", nil, false),
		makeBatchDoc("src/util/buf.h", "en", "#define BUF_MAX 1024\n", nil, false),
		makeBatchDoc("src/server/request_internal.h", "en", "#define ERR_BAD_REQUEST 400\n", nil, false),
		makeBatchDoc("src/arch/memcpy.S", "en", "ENTRY(memcpy_fast)\n\tret\nEND(memcpy_fast)\n", nil, false),
	}); err != nil {
		t.Fatal(err)
	}
}

func edgesOf(res *GraphResult, kind EdgeKind) map[string]bool {
	out := map[string]bool{}
	for _, e := range res.Edges {
		if e.Kind == kind {
			out[e.From+" -> "+e.To] = true
		}
	}
	return out
}

func TestCodeGraphFollowsCCallsAndIncludes(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	loadCProject(t, srv, []string{"src"})

	// "Which files include this header?"
	in := edgesOf(mustGraph(t, srv, GraphRequest{Collection: "myproject", Key: "src/util/buf.h", Direction: GraphIn}), EdgeImports)
	for _, want := range []string{"src/server/request.c -> src/util/buf.h", "src/util/buf.c -> src/util/buf.h"} {
		if !in[want] {
			t.Errorf("missing include edge %s; got %v", want, in)
		}
	}

	// What request.c depends on: the function it calls, defined in another
	// file, as a calls edge; both kinds of include.
	out := mustGraph(t, srv, GraphRequest{Collection: "myproject", Key: "src/server/request.c", Direction: GraphOut})
	if calls := edgesOf(out, EdgeCalls); !calls["src/server/request.c -> src/util/buf.c"] {
		t.Errorf("missing calls edge to buf_alloc's file; got %+v", out.Edges)
	}
	imps := edgesOf(out, EdgeImports)
	for _, want := range []string{"src/server/request.c -> src/net/conn.h", "src/server/request.c -> src/server/request_internal.h"} {
		if !imps[want] {
			t.Errorf("missing include edge %s; got %v", want, imps)
		}
	}

	// "What breaks if I change memcpy_fast": C calling into assembly.
	asmIn := edgesOf(mustGraph(t, srv, GraphRequest{Collection: "myproject", Key: "src/arch/memcpy.S", Direction: GraphIn}), EdgeCalls)
	if !asmIn["src/util/buf.c -> src/arch/memcpy.S"] {
		t.Errorf("missing calls edge from C into assembly; got %v", asmIn)
	}
}

// The document carries exactly the meta the blog post shows, so
// `defines=parse_request` works through the ordinary metadata filter.
func TestCDocumentsGetSymbolMeta(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	loadCProject(t, srv, []string{"src"})

	ref, err := srv.loadCodeDocByKey("myproject", "src/server/request.c")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Language != "c" {
		t.Errorf("language = %q, want c", ref.Language)
	}
	want := map[string][]string{
		"defines": {"parse_header", "parse_request"},
		"uses":    {"buf_alloc", "conn_close", "dispatch"},
		"imports": {"src/net/conn.h", "src/util/buf.h", "src/server/request_internal.h"}, // source order, as the post shows
	}
	got := map[string][]string{"defines": ref.Defines, "uses": ref.Uses, "imports": ref.Imports}
	for k, w := range want {
		if len(got[k]) != len(w) {
			t.Errorf("%s = %v, want %v", k, got[k], w)
			continue
		}
		for i := range w {
			if got[k][i] != w[i] {
				t.Errorf("%s = %v, want %v", k, got[k], w)
			}
		}
	}
}

// Without include paths an angle include is collection-relative, and paths
// that try to leave the collection are cleaned to stay inside it.
func TestIncludePathsDefaultAndCleaning(t *testing.T) {
	if got := resolveImportPaths("src/server/a.c", "c", []string{"<net/conn.h>", "local.h"}, nil); len(got) != 2 || got[0] != "net/conn.h" || got[1] != "src/server/local.h" {
		t.Errorf("no include paths: %v", got)
	}
	if got := resolveImportPaths("a.c", "c", []string{"<x.h>"}, []string{"src", "include"}); len(got) != 2 || got[0] != "src/x.h" || got[1] != "include/x.h" {
		t.Errorf("two include paths: %v", got)
	}

	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := srv.CollectionManager.Set("k", &CollectionConfig{Type: "default", Code: &CodeConfigDef{
		IncludePaths: []string{" /src/ ", "../../etc", ".", ""},
	}}); err != nil {
		t.Fatal(err)
	}
	got := srv.codeIncludePaths("k")
	if len(got) != 2 || got[0] != "src" || got[1] != "etc" {
		t.Errorf("cleaned include paths = %v, want [src etc]", got)
	}
	if srv.codeIncludePaths("unconfigured") != nil {
		t.Error("a collection without code config has include paths")
	}
}

func TestUsesEdgeKindFollowsTheLanguage(t *testing.T) {
	for lang, want := range map[string]EdgeKind{"c": EdgeCalls, "cpp": EdgeCalls, "asm": EdgeCalls, "html": EdgeUsesSelector, "": EdgeUsesSelector} {
		if got := usesEdgeKind(lang); got != want {
			t.Errorf("%q: %s, want %s", lang, got, want)
		}
	}
	for ext, lang := range map[string]string{"a.c": "c", "a.h": "c", "a.S": "asm", "a.s": "asm", "a.cpp": "cpp", "a.hpp": "cpp", "a.m4": "m4"} {
		if got := InferCodeLanguage(ext); got != lang {
			t.Errorf("%s: %q, want %q", ext, got, lang)
		}
	}
}
