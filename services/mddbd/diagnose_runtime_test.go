package main

import (
	"encoding/json"
	"testing"
)

// runtime.json (--profiles) carries the Go runtime's own counters — goroutines,
// heap and GC — as numbers only. Stacks and heap profiles come from
// /debug/pprof (MDDB_PPROF_ENABLED, admin), not from the bundle: writing a
// pprof profile is a debug feature SonarCloud refuses in shipped code
// (go:S4507).
func TestRuntimePartIsNumbersOnly(t *testing.T) {
	raw, err := runtimePart()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"goroutines", "heapAllocBytes", "heapInuseBytes", "heapObjects", "sysBytes", "numGC", "gcPauseTotalNs"} {
		v, ok := got[k].(float64)
		if !ok {
			t.Errorf("%s = %v, want a number", k, got[k])
		}
		if k == "goroutines" && v < 1 {
			t.Errorf("goroutines = %v", v)
		}
	}
	if len(got) != 7 {
		t.Errorf("runtime.json has %d fields, want exactly the 7 counters: %v", len(got), got)
	}
}
