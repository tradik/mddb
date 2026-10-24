package main

import (
	"context"
	"testing"

	proto "mddb/proto"
)

// #319: a kernel tree, one document per file, keyed from the repository root
// as spagu/kernel stores it. No meta is sent: the extension makes it code.

func loadKernelTree(t *testing.T, srv *Server, includePaths []string) {
	t.Helper()
	if includePaths != nil {
		if err := srv.CollectionManager.Set("kernel", &CollectionConfig{
			Type: "default", Code: &CodeConfigDef{IncludePaths: includePaths},
		}); err != nil {
			t.Fatal(err)
		}
	}
	bp := NewBatchProcessor(srv, 2)
	if _, err := bp.ProcessBatch(context.Background(), "kernel", []*proto.BatchDocument{
		makeBatchDoc("sys/kern/kern_exec.c", "en", `
#include <sys/param.h>
#include <sys/proc.h>
static int do_execve(struct thread *td);
int
sys_execve(struct thread *td, struct execve_args *uap)
{
	return (kern_execve(td, uap));
}
`, nil, false),
		makeBatchDoc("sys/kern/kern_proc.c", "en", `
#include <sys/proc.h>
int
kern_execve(struct thread *td, void *a)
{
	return (copyin(a, td, 1));
}
`, nil, false),
		makeBatchDoc("sys/sys/proc.h", "en", "struct proc { int p_pid; };\n#define PROC_LOCK(p) mtx_lock(p)\n", nil, false),
		makeBatchDoc("sys/sys/param.h", "en", "#define MAXPATHLEN 1024\n", nil, false),
		makeBatchDoc("sys/amd64/amd64/support.S", "en", "ENTRY(copyin)\n\tret\nEND(copyin)\n", nil, false),
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
	loadKernelTree(t, srv, []string{"sys"})

	// "Which files include sys/mbuf.h" — here sys/proc.h.
	in := edgesOf(mustGraph(t, srv, GraphRequest{Collection: "kernel", Key: "sys/sys/proc.h", Direction: GraphIn}), EdgeImports)
	for _, want := range []string{"sys/kern/kern_exec.c -> sys/sys/proc.h", "sys/kern/kern_proc.c -> sys/sys/proc.h"} {
		if !in[want] {
			t.Errorf("missing include edge %s; got %v", want, in)
		}
	}

	// What kern_exec.c depends on: the function it calls, defined in another
	// file, as a calls edge; and both headers.
	out := mustGraph(t, srv, GraphRequest{Collection: "kernel", Key: "sys/kern/kern_exec.c", Direction: GraphOut})
	if calls := edgesOf(out, EdgeCalls); !calls["sys/kern/kern_exec.c -> sys/kern/kern_proc.c"] {
		t.Errorf("missing calls edge to kern_execve's file; got %+v", out.Edges)
	}
	if imps := edgesOf(out, EdgeImports); !imps["sys/kern/kern_exec.c -> sys/sys/param.h"] {
		t.Errorf("missing include edge to param.h; got %+v", out.Edges)
	}

	// "What breaks if I change copyin": C calling into assembly.
	asmIn := edgesOf(mustGraph(t, srv, GraphRequest{Collection: "kernel", Key: "sys/amd64/amd64/support.S", Direction: GraphIn}), EdgeCalls)
	if !asmIn["sys/kern/kern_proc.c -> sys/amd64/amd64/support.S"] {
		t.Errorf("missing calls edge from C into assembly; got %v", asmIn)
	}
}

// The documents carry the issue's expected meta, so `defines=sys_execve`
// works through the ordinary metadata filter.
func TestCDocumentsGetSymbolMeta(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	loadKernelTree(t, srv, []string{"sys"})

	ref, err := srv.loadCodeDocByKey("kernel", "sys/kern/kern_exec.c")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Language != "c" {
		t.Errorf("language = %q, want c", ref.Language)
	}
	want := map[string][]string{
		"defines": {"do_execve", "sys_execve"},
		"uses":    {"kern_execve"},
		"imports": {"sys/sys/param.h", "sys/sys/proc.h"},
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
	if got := resolveImportPaths("sys/kern/a.c", "c", []string{"<sys/proc.h>", "local.h"}, nil); len(got) != 2 || got[0] != "sys/proc.h" || got[1] != "sys/kern/local.h" {
		t.Errorf("no include paths: %v", got)
	}
	if got := resolveImportPaths("a.c", "c", []string{"<x.h>"}, []string{"sys", "include"}); len(got) != 2 || got[0] != "sys/x.h" || got[1] != "include/x.h" {
		t.Errorf("two include paths: %v", got)
	}

	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := srv.CollectionManager.Set("k", &CollectionConfig{Type: "default", Code: &CodeConfigDef{
		IncludePaths: []string{" /sys/ ", "../../etc", ".", ""},
	}}); err != nil {
		t.Fatal(err)
	}
	got := srv.codeIncludePaths("k")
	if len(got) != 2 || got[0] != "sys" || got[1] != "etc" {
		t.Errorf("cleaned include paths = %v, want [sys etc]", got)
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
