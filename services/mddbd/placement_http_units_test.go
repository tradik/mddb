package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// GO-052: GET /v1/placement and POST /v1/placement/move.

// mustGroup is the group p places c in.
func mustGroup(p *placementRuntime, c string) string {
	g, _ := p.owner(c)
	return g
}

func placementRequest(s *Server, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if strings.HasPrefix(path, "/v1/placement/move") {
		s.handlePlacementMove(w, r)
	} else {
		s.handlePlacement(w, r)
	}
	return w
}

func TestPlacementHandlersWithoutPlacement(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	cases := []struct {
		method, path, body string
		want               int
		wantBody           string
	}{
		{http.MethodGet, "/v1/placement", "", http.StatusOK, `{"enabled":false}`},
		{http.MethodPost, "/v1/placement", "", http.StatusMethodNotAllowed, ""},
		{http.MethodGet, "/v1/placement/move", "", http.StatusMethodNotAllowed, ""},
		{http.MethodPost, "/v1/placement/move", `{`, http.StatusBadRequest, ""},
		{http.MethodPost, "/v1/placement/move", `{"collection":"c"}`, http.StatusBadRequest, ""},
		{http.MethodPost, "/v1/placement/move", `{"collection":"c","group":"g"}`, http.StatusBadRequest, "not configured"},
	}
	for _, c := range cases {
		w := placementRequest(s, c.method, c.path, c.body)
		if w.Code != c.want || !strings.Contains(w.Body.String(), c.wantBody) {
			t.Errorf("%s %s %s: %d %s", c.method, c.path, c.body, w.Code, w.Body)
		}
	}
}

func TestPlacementHandlersOnTheMetaLeader(t *testing.T) {
	tp := startTestPlacementWith(t, map[string]int{"meta": 1, "g2": 1}, func(s *Server) {
		s.Aliases = NewAliasManager(s.DB)
		if err := s.Aliases.LoadAll(); err != nil {
			t.Fatal(err)
		}
	})
	meta, g2 := tp.leader("meta"), tp.leader("g2")
	p := meta.s.placement
	mine := nameIn(t, p, "meta")
	for _, c := range []string{mine, "second"} {
		if err := meta.add(c, "k", "text"); err != nil {
			t.Fatal(err)
		}
	}
	waitE2E(t, "both collections pinned", func() bool { return len(p.current().Entries) >= 2 })
	if err := meta.s.SetAlias("nick", mine); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		s    *Server
		body string
		want int
	}{
		{"already there, by its alias", meta.s, `{"collection":"nick","group":"meta"}`, http.StatusConflict},
		{"an unknown group", meta.s, `{"collection":"x","group":"nowhere"}`, http.StatusBadRequest},
		{"not the meta leader", g2.s, `{"collection":"x","group":"meta"}`, http.StatusConflict},
	}
	for _, c := range cases {
		if w := placementRequest(c.s, http.MethodPost, "/v1/placement/move", c.body); w.Code != c.want {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}
	// A move already running.
	busy := p.moves.begin("busy", "meta", "g2")
	if w := placementRequest(meta.s, http.MethodPost, "/v1/placement/move", `{"collection":"busy","group":"g2"}`); w.Code != http.StatusConflict {
		t.Errorf("a running move: %d", w.Code)
	}
	p.moves.finish(busy, nil)
	// Without waiting: 202, and the move runs on.
	w := placementRequest(meta.s, http.MethodPost, "/v1/placement/move", `{"collection":"`+mine+`","group":"g2"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("a move: %d %s", w.Code, w.Body)
	}
	waitE2E(t, "the move to end", func() bool { g, _ := p.owner(mine); return g == "g2" })
	// What GET shows, in redirect mode too.
	p.cfg.redirect = true
	defer func() { p.cfg.redirect = false }()
	var got placementJSON
	if err := json.Unmarshal(placementRequest(meta.s, http.MethodGet, "/v1/placement", "").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Route != "redirect" || len(got.Groups) != 2 || len(got.Moves) < 2 || got.Groups[0].Leader == "" {
		t.Fatalf("%+v", got)
	}
}

func TestPlacementMoveThatFailsAnswersConflictWhenWaited(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 1}, "meta")
	meta := tp.leader("meta")
	tp.leader("g2").grpcSrv.Stop() // the target cannot import
	c := nameIn(t, meta.s.placement, "meta")
	other := ""
	for i := 0; other == ""; i++ {
		if n := fmt.Sprintf("left-%d", i); mustGroup(meta.s.placement, n) == "meta" {
			other = n
		}
	}
	w := placementRequest(meta.s, http.MethodPost, "/v1/placement/move?wait=true", `{"collection":"`+c+`","group":"g2"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"phase":"failed"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// A caller that goes away while it waits gets no answer.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodPost, "/v1/placement/move?wait=true",
		strings.NewReader(`{"collection":"`+other+`","group":"g2"}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	meta.s.handlePlacementMove(rec, r)
	if rec.Body.Len() != 0 {
		t.Fatalf("an answer to a caller that left: %s", rec.Body)
	}
}
