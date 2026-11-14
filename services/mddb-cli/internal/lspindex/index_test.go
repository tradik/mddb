package lspindex

import (
	"context"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestIndexHappyPath(t *testing.T) {
	before := runtime.NumGoroutine()
	spec := goSpec(t)
	f := newFakeRun(t, goTree, goScript())
	res, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{MemoryLimitMb: 100}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Server != "gopls/0.24.0" {
		t.Errorf("Server = %q", res.Server)
	}
	if !reflect.DeepEqual(res.Files, goWant) {
		for path, ff := range res.Files {
			t.Logf("%s: %+v", path, *ff)
		}
		t.Fatal("facts differ from goWant")
	}
	st := res.Stats
	st.Elapsed, st.PeakRssMb, st.LoadWait = 0, 0, 0
	want := Stats{Files: 3, Defs: 8, Refs: 7, Impls: 1, CrossFileRefs: 5, Requests: 13, Dropped: 4}
	if st != want {
		t.Errorf("Stats = %+v\nwant    %+v", st, want)
	}
	rec := f.record(t)
	if slices.Contains(rec.Asked, "references a_test.go:3") || !slices.Contains(rec.Asked, "references a_test.go:4") {
		t.Errorf("TestFlush must not be asked for references, Testify must: %v", rec.Asked)
	}
	init := rec.Initialize
	if init["rootUri"] != fileURI(f.realRoot(t)) {
		t.Errorf("rootUri = %v", init["rootUri"])
	}
	if got := init["initializationOptions"]; !reflect.DeepEqual(got, map[string]any{"memoryLimit": float64(100 << 20)}) {
		t.Errorf("initializationOptions = %v", got)
	}
	caps := init["capabilities"].(map[string]any)["textDocument"].(map[string]any)["documentSymbol"]
	kinds := map[string]any{"valueSet": []any{}}
	for k := 1; k <= 26; k++ {
		kinds["valueSet"] = append(kinds["valueSet"].([]any), float64(k))
	}
	if !reflect.DeepEqual(caps, map[string]any{"hierarchicalDocumentSymbolSupport": true, "symbolKind": kinds}) {
		t.Errorf("documentSymbol capabilities = %v", caps)
	}
	assertNoGoroutineLeak(t, before)
}

// realRoot is the root as Index sees it (t.TempDir may sit under a symlink).
func (f *fakeRun) realRoot(t *testing.T) string {
	t.Helper()
	r, err := realRoot(f.root)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestIndexIsDeterministic(t *testing.T) {
	spec := goSpec(t)
	f := newFakeRun(t, goTree, goScript())
	var encoded []map[string][]string
	for range 2 {
		res, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{Concurrency: 8}))
		if err != nil {
			t.Fatal(err)
		}
		all := map[string][]string{}
		for _, path := range sortedKeys(res.Files) {
			for k, v := range Encode(goTree[path], res.Server, res.Files[path]) {
				all[path+" "+k] = v
			}
		}
		encoded = append(encoded, all)
	}
	if !reflect.DeepEqual(encoded[0], encoded[1]) {
		t.Fatalf("two runs differ:\n%v\n%v", encoded[0], encoded[1])
	}
}

func TestIndexAnswersServerRequests(t *testing.T) {
	spec := goSpec(t)
	sc := goScript()
	sc.ServerRequests = []string{"window/workDoneProgress/create", "client/registerCapability",
		"workspace/configuration", "workspace/workspaceFolders", "custom/unknown"}
	f := newFakeRun(t, goTree, sc)
	res, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Files, goWant) {
		t.Error("facts differ from goWant")
	}
	got := map[string]string{}
	for _, a := range f.record(t).Answers {
		m := map[string]any{}
		if err := jsonUnmarshal(a, &m); err != nil {
			t.Fatal(err)
		}
		got[m["id"].(string)] = mustJSON(t, m["result"]) + mustJSON(t, m["error"])
	}
	want := map[string]string{
		"s0": "nullnull", "s1": "nullnull", "s2": "[null,null]null",
		"s3": `[{"name":"root","uri":"` + fileURI(f.realRoot(t)) + `"}]null`,
		"s4": `null{"code":-32601,"message":"not supported by mddb-cli: custom/unknown"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("answers = %v\nwant %v", got, want)
	}
}

func TestIndexRequestTimeoutCancels(t *testing.T) {
	spec := goSpec(t)
	sc := goScript()
	sc.Hang = []string{"references a.go:12"}
	f := newFakeRun(t, goTree, sc)
	res, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{RequestTimeout: 200 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Skipped != 1 || res.Stats.TimedOut != 1 {
		t.Errorf("Stats = %+v", res.Stats)
	}
	if _, ok := res.Files["a.go"]; !ok {
		t.Error("a.go lost with one timed-out symbol")
	}
	if rec := f.record(t); len(rec.Cancelled) != 1 {
		t.Errorf("$/cancelRequest sent for %v", rec.Cancelled)
	}
}

func TestIndexSkipsFailedRequests(t *testing.T) {
	spec := goSpec(t)
	sc := goScript()
	sc.Fail = []string{"documentSymbol b.go", "references a.go:6", "implementation a.go:2"}
	sc.BadResult = []string{"references a.go:8"}
	f := newFakeRun(t, goTree, sc)
	files := append([]string{"missing.go", "/abs.go", "../up.go", "has space.go", "a.go"}, goFiles...)
	res, err := Index(context.Background(), f.root, files, spec, f.options(spec, Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Files["b.go"]; ok {
		t.Error("b.go present although documentSymbol failed")
	}
	// missing.go, /abs.go, ../up.go, documentSymbol b.go, two references,
	// one implementation.
	if res.Stats.Skipped != 7 || res.Stats.Unencodable != 1 || res.Stats.TimedOut != 0 {
		t.Errorf("Stats = %+v", res.Stats)
	}
	f.record(t)
}
