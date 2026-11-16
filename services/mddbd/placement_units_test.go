package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/cluster"

	bolt "go.etcd.io/bbolt"
)

// GO-052: configuration, the map, the keys of a collection and the local
// states of a group's leader, one at a time.

const unitGroups = `{"meta":"meta","groups":{"meta":[{"id":"a","grpc":"h:1","http":"http://h:2"}],"g2":[{"id":"b","grpc":"h:3","http":"https://h:4"}]}}`

func TestPlacementConfigFromEnv(t *testing.T) {
	t.Setenv("MDDB_PLACEMENT_GROUPS", "")
	if _, ok, err := placementConfigFromEnv(); ok || err != nil {
		t.Fatalf("unset: %v %v", ok, err)
	}
	t.Setenv("MDDB_PLACEMENT_GROUPS", unitGroups)
	t.Setenv("MDDB_PLACEMENT_ROUTE", "redirect")
	t.Setenv("MDDB_PLACEMENT_REFRESH", "250ms")
	pc, ok, err := placementConfigFromEnv()
	if !ok || err != nil || !pc.redirect || pc.refresh != 250*time.Millisecond || pc.dropGrace != defaultDropGrace || pc.meta != "meta" {
		t.Fatalf("inline: %+v %v %v", pc, ok, err)
	}
	file := filepath.Join(t.TempDir(), "groups.json")
	if err := os.WriteFile(file, []byte(unitGroups), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MDDB_PLACEMENT_GROUPS", "@"+file)
	if pc, _, err := placementConfigFromEnv(); err != nil || len(pc.groups) != 2 {
		t.Fatalf("file: %+v %v", pc, err)
	}
	// A missing file is matched as os.ErrNotExist: its message is the OS's
	// own ("no such file" on Unix, "cannot find the file" on Windows).
	t.Run("MDDB_PLACEMENT_GROUPS=@missing", func(t *testing.T) {
		t.Setenv("MDDB_PLACEMENT_GROUPS", "@"+file+".missing")
		if _, _, err := placementConfigFromEnv(); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("got %v, want a missing file", err)
		}
	})
	for _, c := range []struct{ env, value, want string }{
		{"MDDB_PLACEMENT_GROUPS", "{not json", "MDDB_PLACEMENT_GROUPS"},
		{"MDDB_PLACEMENT_ROUTE", "sideways", "use forward or redirect"},
		{"MDDB_PLACEMENT_FENCE_TIMEOUT", "-1s", "positive duration"},
	} {
		t.Run(c.env+"="+c.value, func(t *testing.T) {
			t.Setenv("MDDB_PLACEMENT_GROUPS", unitGroups)
			t.Setenv(c.env, c.value)
			if _, _, err := placementConfigFromEnv(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

func TestPlacementConfigValidate(t *testing.T) {
	good := func() placementConfig {
		return placementConfig{meta: "meta", groups: map[string][]placementNode{
			"meta": {{ID: "a", GRPC: "h:1", HTTP: "http://h:2"}},
			"g2":   {{ID: "b", GRPC: "h:3", HTTP: "http://h:4"}, {ID: "c", GRPC: "h:5", HTTP: "http://h:6"}},
		}}
	}
	peers := []cluster.Peer{{ID: "b", Addr: "h:3"}, {ID: "c", Addr: "h:5"}}
	pc := good()
	if err := pc.validate("b", peers); err != nil || pc.self != "g2" || strings.Join(pc.names, ",") != "g2,meta" {
		t.Fatalf("good: %v %+v", err, pc)
	}
	if n, g, ok := pc.node("c"); !ok || g != "g2" || n.GRPC != "h:5" {
		t.Fatalf("node c: %+v %s %v", n, g, ok)
	}
	if _, _, ok := pc.node("nobody"); ok {
		t.Fatal("an unknown node was found")
	}
	cases := []struct {
		name   string
		change func(*placementConfig)
		self   string
		peers  []cluster.Peer
		want   string
	}{
		{"no groups", func(pc *placementConfig) { pc.groups = nil }, "b", peers, "names no group"},
		{"no meta", func(pc *placementConfig) { pc.meta = "elsewhere" }, "b", peers, "meta group"},
		{"empty group", func(pc *placementConfig) { pc.groups["g3"] = nil }, "b", peers, "needs a name and nodes"},
		{"bad node", func(pc *placementConfig) { pc.groups["g3"] = []placementNode{{ID: "d", GRPC: "h:7", HTTP: "ftp://x"}} }, "b", peers, "http(s) URL"},
		{"twice", func(pc *placementConfig) { pc.groups["g3"] = []placementNode{{ID: "a", GRPC: "h:7", HTTP: "http://x"}} }, "b", peers, "is in groups"},
		{"not in a group", func(*placementConfig) {}, "z", peers, "is in no group"},
		{"fewer peers", func(*placementConfig) {}, "b", peers[:1], "must be the same nodes"},
		{"other address", func(*placementConfig) {}, "b", []cluster.Peer{{ID: "b", Addr: "h:3"}, {ID: "c", Addr: "h:9"}}, "MDDB_CLUSTER_PEERS says"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pc := good()
			c.change(&pc)
			if err := pc.validate(c.self, c.peers); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

func TestPlacementInitRefusals(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	if err := s.initPlacement(placementConfig{}); !errors.Is(err, errPlacementLayout) {
		t.Fatalf("single layout: %v", err)
	}
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	if err := s.initPlacement(placementConfig{}); err == nil || !strings.Contains(err.Error(), "MDDB_CLUSTER_PEERS") {
		t.Fatalf("no cluster: %v", err)
	}
	// Without the variable nothing happens; with a bad one the start stops.
	t.Setenv("MDDB_PLACEMENT_GROUPS", "")
	s.initPlacementFromEnv()
	if s.placement != nil {
		t.Fatal("placement without configuration")
	}
	for _, v := range []string{"{bad", unitGroups} {
		t.Setenv("MDDB_PLACEMENT_GROUPS", v)
		if c := covAExpectFatal(t, s.initPlacementFromEnv); !strings.Contains(c.msg, "Invalid placement configuration") {
			t.Fatalf("fatal: %+v", c)
		}
	}
}

func TestPlacementInitRefusesAConfigurationNotMatchingTheCluster(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	n := tp.nodes[0]
	pc := tp.cfg
	pc.groups = map[string][]placementNode{"meta": {{ID: n.id, GRPC: "elsewhere:1", HTTP: n.http.URL}}}
	if err := n.s.initPlacement(pc); err == nil || !strings.Contains(err.Error(), "MDDB_CLUSTER_PEERS says") {
		t.Fatalf("mismatch: %v", err)
	}
	(&placementRuntime{}).shutdown() // never started: nothing to stop
	// The server's shutdown stops the placement loop before the cluster.
	idle := &Server{placement: &placementRuntime{}}
	for _, step := range idle.shutdownSteps() {
		if step.name == "placement" {
			step.fn()
		}
	}
	if got := n.s.placement.knownLeaderHTTP("meta"); got != n.http.URL {
		t.Fatalf("own leader: %q", got)
	}
}

func TestPlacementSettingsKeys(t *testing.T) {
	cases := []struct {
		bucket, key, value, want string
	}{
		{"colmeta", "blog", "", "blog"},
		{"colmeta", "", "", ""},
		{"schemas", "schema|blog", "", "blog"},
		{"schemas", "other|blog", "", ""},
		{"synonyms", "syn|blog|car", "", "blog"},
		{"stopwords", "sw|blog|the", "", "blog"},
		{"spelldicts", "col|blog|en|word", "", "blog"},
		{"spelldicts", "en|word", "", ""},
		{"curation", "rule-1", `{"collection":"blog"}`, "blog"},
		{"curation", "rule-2", `not json`, ""},
		{"webhooks", "x", "", ""},
	}
	for _, c := range cases {
		got, ok := settingsCollection(c.bucket, []byte(c.key), []byte(c.value))
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s %q: %q %v, want %q", c.bucket, c.key, got, ok, c.want)
		}
	}
	entries := map[string]*binlog.BinlogEntry{
		"blog": {BucketName: "docs", Key: []byte("doc|blog|blog|k|en")},
		"news": {BucketName: "synonyms", Key: []byte("syn|news|car")},
		"":     {BucketName: "auth_users", Key: []byte("admin")},
	}
	for want, e := range entries {
		if got := entryCollection(e); got != want {
			t.Errorf("%s/%s: %q, want %q", e.BucketName, e.Key, got, want)
		}
	}
}

func TestPlacementEntryGate(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 1}, "meta")
	p := tp.leader("meta").s.placement
	mine, theirs := nameIn(t, p, "meta"), nameIn(t, p, "g2")
	put := func(c string) []*binlog.BinlogEntry {
		return []*binlog.BinlogEntry{{BucketName: "docs", Key: []byte("doc|" + c + "|x")},
			{BucketName: "idxmeta", Key: []byte("meta|" + c + "|x")}, {BucketName: "placement", Key: []byte("map")}}
	}
	if err := p.entryGate(put(mine)); err != nil {
		t.Fatalf("own collection: %v", err)
	}
	var elsewhere *collectionElsewhereError
	if err := p.entryGate(put(theirs)); !errors.As(err, &elsewhere) || elsewhere.Group != "g2" ||
		!strings.Contains(err.Error(), "send the request to "+tp.leader("g2").http.URL) {
		t.Fatalf("another group's: %v", err)
	}
	p.local.mark(p.local.importing, theirs, true)
	if err := p.entryGate(put(theirs)); err != nil {
		t.Fatalf("an import: %v", err)
	}
	p.local.mark(p.local.importing, theirs, false)
	stopLoop(p)
	if err := p.local.fence(mine, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := p.entryGate(put(mine)); err == nil || !strings.Contains(err.Error(), "is being placed") {
		t.Fatalf("fenced: %v", err)
	}
	p.local.lift(mine)
	fresh := unitRuntime(tp.leader("meta"), nil, nil)
	fresh.table.Store(nil)
	if err := fresh.admitsWrite(mine); err == nil {
		t.Fatal("a node without a map took a write")
	}
	unknown := &collectionElsewhereError{Collection: "c", Group: "g9"}
	if !strings.Contains(unknown.Error(), "leader is not known yet") {
		t.Fatal(unknown.Error())
	}
}

func TestPlacementFenceWaitsForRunningWrites(t *testing.T) {
	var l localCollections
	l.init()
	end, _ := l.enter("c")
	if err := l.fence("c", 30*time.Millisecond); !errors.Is(err, errFenceBusy) {
		t.Fatalf("a write that does not end: %v", err)
	}
	again, wait := l.enter("c")
	if wait != nil {
		t.Fatal("the failed fence was not lifted")
	}
	again()
	end()
	end2, _ := l.enter("c")
	done := make(chan error, 1)
	go func() { done <- l.fence("c", time.Second) }()
	time.Sleep(20 * time.Millisecond)
	end2()
	if err := <-done; err != nil || !l.isGated("c") {
		t.Fatalf("fence after the write ended: %v gated %v", err, l.isGated("c"))
	}
	if err := l.fence("c", time.Second); err != nil { // fencing again is fine
		t.Fatal(err)
	}
	if got := l.fencedNames(); len(got) != 1 || got[0] != "c" {
		t.Fatalf("fenced: %v", got)
	}
	l.lift("c")
	l.lift("c")
}

func TestPlacementEnterWriteWaits(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	p := unitRuntime(tp.nodes[0], nil, nil)
	p.cfg.fenceTimeout = 40 * time.Millisecond
	_ = p.local.fence("c", time.Second)
	if _, _, err := p.enterWrite(context.Background(), "c"); !errors.Is(err, errFenceHeld) {
		t.Fatalf("held past the timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := p.enterWrite(ctx, "c"); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller gone: %v", err)
	}
	p.cfg.fenceTimeout = time.Second
	go func() {
		time.Sleep(20 * time.Millisecond)
		p.local.lift("c")
	}()
	end, again, err := p.enterWrite(context.Background(), "c")
	if err != nil || again || end == nil {
		t.Fatalf("after the lift: %v %v", again, err)
	}
	end()
}

func TestPlacementMapOrderAndStorage(t *testing.T) {
	a := &placementTable{Version: 5, Term: 2}
	if !(&placementTable{Version: 1, Term: 3}).newerThan(a) || (&placementTable{Version: 9, Term: 1}).newerThan(a) ||
		!(&placementTable{Version: 6, Term: 2}).newerThan(a) {
		t.Fatal("maps are ordered by term, then version")
	}
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	p := tp.nodes[0].s.placement
	// The test writes the stored map itself; a loop round in between would
	// rewrite it (a pin, a new version) under the assertions.
	stopLoop(p)
	// A stored map whose entries are null reads as empty.
	writeStoredMap(t, p, `{"version":1,"term":1,"entries":null}`)
	if got := p.storedMap(); got.Entries == nil {
		t.Fatal("null entries")
	}
	if _, err := p.changeMap(func(t *placementTable) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	writeStoredMap(t, p, `{not json`)
	if _, err := p.changeMap(func(*placementTable) (bool, error) { return true, nil }); err == nil {
		t.Fatal("a corrupt stored map was changed")
	}
	writeStoredMap(t, p, `{"version":2,"term":1}`)
	refused := errors.New("no")
	if _, err := p.changeMap(func(*placementTable) (bool, error) { return false, refused }); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if got, err := p.changeMap(func(*placementTable) (bool, error) { return false, nil }); err != nil || got.Version != 2 {
		t.Fatalf("nothing to write: %+v %v", got, err)
	}
	if p.adopt(nil) {
		t.Fatal("adopted nothing")
	}
}

// writeStoredMap puts raw as the stored map, unlogged.
func writeStoredMap(t *testing.T, p *placementRuntime, raw string) {
	t.Helper()
	if err := p.s.DBUpdate(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketPlacement)
		if err != nil {
			return err
		}
		return b.Put(keyPlacementMap, []byte(raw))
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPlacementMoveBoard(t *testing.T) {
	var b moveBoard
	b.init()
	m := b.begin("c", "a", "b")
	if b.begin("c", "a", "b") != nil || !b.running("c") {
		t.Fatal("a second move of the same collection began")
	}
	if got := b.list(); len(got) != 1 || got[0].Phase != movePhaseCopying {
		t.Fatalf("running: %+v", got)
	}
	b.finish(m, errors.New("broken"))
	if st := b.get(m); st.Phase != movePhaseFailed || st.Error != "broken" || b.running("c") {
		t.Fatalf("failed: %+v", st)
	}
	for range recentMoves + 3 {
		b.finish(b.begin("d", "a", "b"), nil)
	}
	if got := b.list(); len(got) != recentMoves || got[len(got)-1].Phase != movePhaseDone {
		t.Fatalf("kept %d moves", len(got))
	}
}
