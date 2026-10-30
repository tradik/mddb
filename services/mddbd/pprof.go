package main

import (
	"net/http/pprof"
)

// registerPprof registers Go pprof profiling endpoints on the given mux.
// Enabled via MDDB_PPROF_ENABLED=true (disabled by default for security).
//
// Endpoints:
//
//	GET /debug/pprof/           - Index page with all profiles
//	GET /debug/pprof/cmdline    - Command line arguments
//	GET /debug/pprof/profile    - CPU profile (30s default, ?seconds=N)
//	GET /debug/pprof/symbol     - Symbol lookup
//	GET /debug/pprof/trace      - Execution trace (?seconds=N)
//	GET /debug/pprof/heap       - Heap memory profile
//	GET /debug/pprof/goroutine  - Goroutine stack dumps
//	GET /debug/pprof/allocs     - Allocation profile
//	GET /debug/pprof/block      - Block (contention) profile
//	GET /debug/pprof/mutex      - Mutex contention profile
//	GET /debug/pprof/threadcreate - Thread creation profile
func registerPprof(mux routeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}
