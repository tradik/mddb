package main

import (
	"net/http"
	"strings"
	"testing"
)

// GO-052: placement status and placement move.

// placementBody is a GET /v1/placement answer with every section filled.
func placementBody() map[string]interface{} {
	return map[string]interface{}{
		"enabled": true, "nodeId": "b1", "group": "g2", "metaGroup": "meta",
		"version": 1731800000000000000, "term": 3, "route": "forward",
		"groups": []map[string]interface{}{
			{"name": "meta", "meta": true, "leader": "a1", "leaderHttp": "http://a1:11023", "collections": 10,
				"nodes": []map[string]interface{}{{"id": "a1", "http": "http://a1:11023", "grpc": "a1:11024"}, {"id": "a2"}}},
			{"name": "g2", "leader": "", "collections": 0},
		},
		"collections": []map[string]interface{}{
			{"name": "blog", "group": "g1", "movingTo": "g2"},
			{"name": "docs", "group": "g2", "movingTo": ""},
		},
		"moves": []map[string]interface{}{
			{"collection": "blog", "from": "g1", "to": "g2", "phase": "copying", "keysCopied": 1234, "sourceLsn": 999, "appliedLsn": 950},
			{"collection": "docs", "from": "g1", "to": "g2", "phase": "done", "keysCopied": 7, "durationMs": 1500},
			{"collection": "logs", "from": "g2", "to": "g3", "phase": "failed", "error": "target group unreachable"},
		},
	}
}

func TestPlacementStatus(t *testing.T) {
	cases := []struct {
		name    string
		route   interface{}
		args    []string
		want    []string
		notWant []string
		wantErr bool
	}{
		{name: "full report", route: placementBody(), args: []string{"placement"},
			want: []string{"Node:       b1", "Group:      g2", "Meta group: meta", "Version:    1731800000000000000 (term 3)",
				"Route:      forward", "a1,a2", "*", "blog", "g1 → g2", "1234", "950/999", "1.5s", "target group unreachable", "done"}},
		{name: "status alias", route: placementBody(), args: []string{"placement", "status"}, want: []string{"Meta group: meta"}},
		{name: "unknown leader and empty group", route: placementBody(), args: []string{"placement"},
			want: []string{"g2                 -                      0  -"}},
		{name: "empty sections", route: map[string]interface{}{"enabled": true, "nodeId": "b1"}, args: []string{"placement"},
			want: []string{"Node:       b1"}, notWant: []string{"Collection", "Phase", "Leader"}},
		{name: "not configured", route: map[string]interface{}{"enabled": false}, args: []string{"placement"},
			want: []string{"not configured", "MDDB_PLACEMENT_GROUPS"}, notWant: []string{"Node:"}},
		{name: "json", route: placementBody(), args: []string{"placement", "--json"}, want: []string{`"nodeId":"b1"`}},
		{name: "broken body", route: "{", args: []string{"placement"}, wantErr: true},
		{name: "refused", route: failure{http.StatusForbidden, `{"error":"no"}`}, args: []string{"placement", "status"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeServer(t, map[string]interface{}{"GET /v1/placement": tc.route})
			out, err := runCLI(t, fs.URL, tc.args...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v\n%s", err, tc.wantErr, out)
			}
			for _, w := range tc.want {
				mustContain(t, out, w)
			}
			for _, nw := range tc.notWant {
				if strings.Contains(out, nw) {
					t.Errorf("output mentions %q:\n%s", nw, out)
				}
			}
		})
	}
}

func TestPlacementMove(t *testing.T) {
	started := map[string]interface{}{"move": map[string]interface{}{
		"collection": "blog", "from": "g1", "to": "g3", "phase": "copying"}}
	done := map[string]interface{}{"move": map[string]interface{}{
		"collection": "blog", "from": "g1", "to": "g3", "phase": "done", "keysCopied": 1234, "durationMs": 2500}}
	failed := failure{http.StatusConflict, `{"error":"catch-up timed out","move":{"collection":"blog","from":"g1","to":"g3","phase":"failed","error":"catch-up timed out"}}`}

	cases := []struct {
		name     string
		route    interface{}
		args     []string
		wantPath string
		want     []string
		errText  string // non-empty: the command fails with this text
	}{
		{name: "started", route: started, args: []string{"blog", "g3"}, wantPath: "/v1/placement/move",
			want: []string{"Moving blog from g1 to g3: copying"}},
		{name: "started json", route: started, args: []string{"blog", "g3", "--json"}, want: []string{`"phase":"copying"`}},
		{name: "wait done", route: done, args: []string{"blog", "g3", "--wait"}, wantPath: "/v1/placement/move",
			want: []string{"Move of blog from g1 to g3: done, 1234 keys copied in 2.5s"}},
		{name: "wait failed", route: failed, args: []string{"blog", "g3", "--wait", "--timeout", "5m"},
			errText: "move of blog from g1 to g3 failed: catch-up timed out"},
		{name: "wait failed json", route: failed, args: []string{"blog", "g3", "--wait", "--json"},
			want: []string{`"phase":"failed"`}, errText: "catch-up timed out"},
		{name: "already there", route: failure{http.StatusConflict, `{"error":"blog is already in g3"}`},
			args: []string{"blog", "g3"}, errText: "already in g3"},
		{name: "conflict not json", route: failure{http.StatusConflict, `nope`}, args: []string{"blog", "g3"}, errText: "409"},
		{name: "unknown group", route: failure{http.StatusBadRequest, `{"error":"unknown group g9"}`},
			args: []string{"blog", "g9"}, errText: "unknown group g9"},
		{name: "broken body", route: "{", args: []string{"blog", "g3"}, errText: "parse response"},
		{name: "no move", route: map[string]interface{}{}, args: []string{"blog", "g3"}, errText: "no move"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeServer(t, map[string]interface{}{"POST /v1/placement/move": tc.route})
			out, err := runCLI(t, fs.URL, append([]string{"placement", "move"}, tc.args...)...)
			checkMoveOutcome(t, err, tc.errText, out)
			for _, w := range tc.want {
				mustContain(t, out, w)
			}
			c := fs.lastCall(t)
			assertBodyField(t, c.Body, "collection", "blog")
			assertBodyField(t, c.Body, "group", tc.args[1])
			if tc.wantPath != "" && c.Path != tc.wantPath {
				t.Errorf("path = %s, want %s", c.Path, tc.wantPath)
			}
		})
	}
}

// checkMoveOutcome asserts the command succeeded, or failed with errText.
func checkMoveOutcome(t *testing.T, err error, errText, out string) {
	t.Helper()
	if errText == "" {
		if err != nil {
			t.Fatalf("unexpected error %v\n%s", err, out)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), errText) {
		t.Fatalf("err = %v, want it to mention %q", err, errText)
	}
}

func TestPlacementMoveSendsWait(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"POST /v1/placement/move?wait=true": map[string]interface{}{"move": map[string]interface{}{"phase": "done"}},
		"POST /v1/placement/move":           failure{http.StatusTeapot, `{"error":"wait was not sent"}`},
	})
	if out, err := runCLI(t, fs.URL, "placement", "move", "blog", "g3", "--wait"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := runCLI(t, fs.URL, "placement", "move", "blog"); err == nil {
		t.Fatal("a move without a group was accepted")
	}
}
