package main

import (
	"net/http"
	"strings"
	"testing"
)

// GO-051: replication status, promote and demote.

func TestReplicationStatusShowsTheCluster(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/replication/status": map[string]interface{}{
			"node_id": "n1", "role": "leader", "current_lsn": 42,
			"cluster": map[string]interface{}{
				"term": 5, "leader_id": "n1", "leader_addr": "http://n1:11023", "writable": true,
				"last_rejected_file": "/data/rejected-writes-n1-term4.jsonl",
				"nodes": []map[string]interface{}{
					{"id": "n1", "state": "leader", "term": 5, "last_lsn": 42, "reachable": true},
					{"id": "n2", "state": "follower", "term": 5, "last_lsn": 40, "reachable": false},
				},
			},
		},
	})
	out, err := runCLI(t, fs.URL, "replication", "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"n1", "leader", "Term:     5", "http://n1:11023", "Writable: true", "rejected-writes-n1-term4", "n2", "follower"} {
		mustContain(t, out, want)
	}
	out, err = runCLI(t, fs.URL, "replication", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, `"node_id":"n1"`)
}

func TestReplicationStatusOutsideACluster(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/replication/status": map[string]interface{}{"node_id": "x", "role": "standalone"},
	})
	out, err := runCLI(t, fs.URL, "replication", "status")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "standalone")
	if strings.Contains(out, "Term:") {
		t.Fatalf("a standalone node shows a term:\n%s", out)
	}
	fs = newFakeServer(t, map[string]interface{}{
		"/v1/replication/status": map[string]interface{}{"cluster": map[string]interface{}{"term": 1}},
	})
	if out, err = runCLI(t, fs.URL, "replication", "status"); err != nil || !strings.Contains(out, "Term:     1") {
		t.Fatalf("%v\n%s", err, out)
	}
	fs = newFakeServer(t, map[string]interface{}{"/v1/replication/status": "{"})
	if _, err := runCLI(t, fs.URL, "replication", "status"); err == nil {
		t.Fatal("a broken response")
	}
	fs = newFakeServer(t, map[string]interface{}{"/v1/replication/status": failure{http.StatusForbidden, `{"error":"no"}`}})
	if _, err := runCLI(t, fs.URL, "replication", "status"); err == nil {
		t.Fatal("a refused request")
	}
}

func TestReplicationPromoteAndDemote(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/replication/promote": map[string]interface{}{"node_id": "n2", "state": "leader", "term": 6},
		"/v1/replication/demote":  map[string]interface{}{"node_id": "n2", "state": "follower", "term": 6},
	})
	out, err := runCLI(t, fs.URL, "replication", "promote", "--force")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "n2 is leader in term 6")
	if c := fs.lastCall(t); c.Method != http.MethodPost || c.Body["force"] != true {
		t.Fatalf("sent %+v", c)
	}
	out, err = runCLI(t, fs.URL, "replication", "demote", "--json")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, `"state":"follower"`)

	fs = newFakeServer(t, map[string]interface{}{
		"/v1/replication/promote": failure{http.StatusConflict, `{"error":"another node has a later log"}`},
		"/v1/replication/demote":  "not json",
	})
	if _, err := runCLI(t, fs.URL, "replication", "promote"); err == nil {
		t.Fatal("a refused promotion reported success")
	}
	if _, err := runCLI(t, fs.URL, "replication", "demote"); err == nil {
		t.Fatal("a broken answer reported success")
	}
}
