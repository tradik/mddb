package main

import (
	"strings"
	"testing"
)

// covHSeedCallees stores a function calling three helpers and a file one
// directory deeper.
func covHSeedCallees(t *testing.T, s *Server) {
	t.Helper()
	for key, content := range map[string]string{
		"pkg/main.go":     "package pkg\n\nfunc Run() {\n\ta()\n\tb()\n\tc()\n}\n",
		"pkg/helpers.go":  "package pkg\n\nfunc a() {\n}\n\nfunc b() {\n}\n\nfunc c() {\n}\n",
		"pkg/sub/deep.go": "package sub\n\nfunc Deep() {\n}\n",
	} {
		addTestDoc(t, s, "repo", key, "en", content, nil)
	}
}

func TestCovHSymbolCalleesStopAtTheLimit(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	covHSeedCallees(t, s)

	all := lookup(t, s, SymbolRequest{Collection: "repo", Name: "Run", Include: []string{RelCallees}})
	if got := passageKeys(all.Callees); !strings.Contains(got, "@a") || !strings.Contains(got, "@c") {
		t.Fatalf("all callees: %s", got)
	}
	// With a limit of one the lookup stops once it has more than enough to
	// report truncation; the last callee is never looked up.
	res := lookup(t, s, SymbolRequest{Collection: "repo", Name: "Run", Include: []string{RelCallees}, Limit: 1})
	if got := passageKeys(res.Callees); strings.Contains(got, "@c") || !strings.Contains(got, "@a") {
		t.Fatalf("limited callees: %s", got)
	}
}

func TestCovHEachSourceInDir(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	covHSeedCallees(t, s)

	// Only files directly in the directory, in key order.
	var keys []string
	s.eachSourceInDir("repo", "pkg", func(st *sourceText) bool {
		keys = append(keys, st.doc.Key)
		return true
	})
	if strings.Join(keys, ",") != "pkg/helpers.go,pkg/main.go" {
		t.Fatalf("visited: %v", keys)
	}

	// A visit that returns false stops the scan.
	visits := 0
	s.eachSourceInDir("repo", "pkg", func(*sourceText) bool {
		visits++
		return false
	})
	if visits != 1 {
		t.Fatalf("visits after stop: %d", visits)
	}

	// A database without the key index has nothing to visit.
	s.BucketNames.ByKey = []byte("covH-missing")
	s.eachSourceInDir("repo", "pkg", func(*sourceText) bool {
		t.Fatal("visited without a key index")
		return true
	})
}
