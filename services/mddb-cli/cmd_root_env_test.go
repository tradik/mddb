package main

import (
	"io"
	"testing"
)

// MDDB_SERVER is the server when --server is not given; --server wins when it
// is; without either the CLI uses localhost.
func TestServerFromEnvironment(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{"/v1/stats": `{"totalDocuments":1}`})
	t.Setenv(serverEnv, fs.URL)

	root := newRootCmd()
	root.SetArgs([]string{"stats"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SilenceUsage, root.SilenceErrors = true, true
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if serverURL != fs.URL || len(callsTo(fs, "GET", "/v1/stats")) != 1 {
		t.Fatalf("server %q, stats calls %d", serverURL, len(callsTo(fs, "GET", "/v1/stats")))
	}

	if root := newRootCmd(); root.PersistentFlags().Lookup("server").DefValue != fs.URL {
		t.Errorf("--server default does not show MDDB_SERVER")
	}

	t.Setenv(serverEnv, "")
	if got := defaultServer(); got != defaultServerURL {
		t.Errorf("default without MDDB_SERVER = %q", got)
	}
}
