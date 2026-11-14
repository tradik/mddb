package lspindex

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestIndexWaitsForProgressToEnd(t *testing.T) {
	spec := goSpec(t)
	sc := goScript()
	sc.ProgressEndMs = 700
	f := newFakeRun(t, goTree, sc)
	res, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{}))
	if err != nil {
		t.Fatal(err)
	}
	asked := f.record(t).Asked
	end := slices.Index(asked, "progress end")
	first := slices.IndexFunc(asked, func(k string) bool { return strings.HasPrefix(k, "references ") })
	if end < 0 || first < end {
		t.Errorf("references asked before the server finished loading: %v", asked)
	}
	if res.Stats.LoadWait < 600*time.Millisecond {
		t.Errorf("LoadWait = %v", res.Stats.LoadWait)
	}
}

func TestIndexDoesNotWaitForeverOnProgress(t *testing.T) {
	old := maxLoadWait
	maxLoadWait = 300 * time.Millisecond
	defer func() { maxLoadWait = old }()
	spec := goSpec(t)
	sc := goScript()
	sc.ProgressOpen = true
	f := newFakeRun(t, goTree, sc)
	if _, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{})); err != nil {
		t.Fatal(err)
	}
}

func TestIndexTimeoutWhileWaitingForProgress(t *testing.T) {
	spec := goSpec(t)
	sc := goScript()
	sc.ProgressOpen = true
	f := newFakeRun(t, goTree, sc)
	res, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{Timeout: 800 * time.Millisecond}))
	_ = wantServerError(t, res, err)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}

func TestWaitIdleEndsWithTheStream(t *testing.T) {
	c, _, _, stop := pipeConn(t)
	c.trackProgress(json.RawMessage(`{"token":"t","value":{"kind":"begin"}}`))
	c.trackProgress(json.RawMessage(`not json`))
	stop()
	if err := c.waitIdle(context.Background(), maxLoadWait); err != nil {
		t.Errorf("err = %v", err)
	}
}
