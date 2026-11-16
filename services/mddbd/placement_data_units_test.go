package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/cache"
	"mddb/internal/geo"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// GO-052: the placement's messages between nodes, the export and import of
// a collection, dropping a copy and the global settings — their refusals
// and failures.

// fakeServerStream is a server stream that keeps what is sent, and fails a send
// once failAt messages were sent (failAt < 0: never).
type fakeServerStream[T any] struct {
	grpc.ServerStream
	ctx    context.Context
	sent   []*T
	failAt int
}

func (f *fakeServerStream[T]) Context() context.Context { return f.ctx }

func (f *fakeServerStream[T]) Send(m *T) error {
	if f.failAt >= 0 && len(f.sent) >= f.failAt {
		return errors.New("stream closed")
	}
	f.sent = append(f.sent, m)
	return nil
}

// peerCtx is a context authorised as a replication peer.
func peerCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-mddb-replication-secret", e2eSecret))
}

// groupsOfTwo is a meta group of one node and a group g2 of two.
func groupsOfTwo(t *testing.T) (*testPlacement, *placementNodeT, *placementNodeT) {
	t.Helper()
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 2}, "meta")
	leader := tp.leader("g2")
	for _, n := range tp.nodes {
		if n.group == "g2" && n != leader {
			return tp, leader, n
		}
	}
	t.Fatal("g2 has no follower")
	return nil, nil, nil
}

func TestPlacementRPCRefusals(t *testing.T) {
	tp, g2, follower := groupsOfTwo(t)
	meta := tp.leader("meta")
	rs := meta.s.replServer.Load()
	bare := context.Background()
	if _, err := rs.GetPlacement(bare, &proto.GetPlacementRequest{}); err == nil {
		t.Fatal("an unauthorised peer read the map")
	}
	if _, err := rs.PushPlacement(bare, &proto.PlacementMap{}); err == nil {
		t.Fatal("an unauthorised peer pushed a map")
	}
	if _, err := rs.PinCollections(bare, &proto.PinCollectionsRequest{}); err == nil {
		t.Fatal("an unauthorised peer pinned")
	}
	if _, err := rs.FenceCollection(bare, &proto.FenceCollectionRequest{}); err == nil {
		t.Fatal("an unauthorised peer fenced")
	}
	plain, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if _, err := NewReplicationServer(plain).GetPlacement(peerCtx(), &proto.GetPlacementRequest{}); !errors.Is(err, errNoPlacement) {
		t.Fatalf("no placement: %v", err)
	}
	plain.placement = unitRuntime(meta, nil, nil)
	plain.placement.table.Store(nil)
	if _, err := NewReplicationServer(plain).GetPlacement(peerCtx(), &proto.GetPlacementRequest{}); err == nil {
		t.Fatal("answered without a map")
	}
	if _, err := g2.s.replServer.Load().PinCollections(peerCtx(), &proto.PinCollectionsRequest{Group: "g2"}); err == nil {
		t.Fatal("a node that does not lead the meta group pinned")
	}
	if _, err := rs.PinCollections(peerCtx(), &proto.PinCollectionsRequest{Group: "nowhere"}); err == nil {
		t.Fatal("pinned into an unknown group")
	}
	writeStoredMap(t, meta.s.placement, `{corrupt`)
	if _, err := rs.PinCollections(peerCtx(), &proto.PinCollectionsRequest{Group: "g2", Collections: []string{"x"}}); err == nil {
		t.Fatal("pinned over a corrupt map")
	}
	writeStoredMap(t, meta.s.placement, `{"version":1,"term":1}`)

	mine, theirs := nameIn(t, meta.s.placement, "meta"), nameIn(t, meta.s.placement, "g2")
	if _, err := follower.s.replServer.Load().FenceCollection(peerCtx(), &proto.FenceCollectionRequest{Collection: theirs}); err == nil {
		t.Fatal("a follower fenced")
	}
	if _, err := rs.FenceCollection(peerCtx(), &proto.FenceCollectionRequest{Collection: theirs}); err == nil {
		t.Fatal("fenced another group's collection")
	}
	p := meta.s.placement
	end, _ := p.local.enter(mine)
	p.cfg.fenceTimeout = 30 * time.Millisecond
	if _, err := rs.FenceCollection(peerCtx(), &proto.FenceCollectionRequest{Collection: mine}); err == nil {
		t.Fatal("fenced over a write that does not end")
	}
	end()
	if _, err := rs.FenceCollection(peerCtx(), &proto.FenceCollectionRequest{Collection: mine}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.FenceCollection(peerCtx(), &proto.FenceCollectionRequest{Collection: mine, Release: true}); err != nil || p.local.isGated(mine) {
		t.Fatalf("release: %v", err)
	}
}

func TestPlacementExportRefusalsAndFailures(t *testing.T) {
	tp, g2, follower := groupsOfTwo(t)
	meta := tp.leader("meta")
	c := nameIn(t, g2.s.placement, "g2")
	for i := range exportChunkSize + 10 {
		if err := writeDoc(g2.s, c, i); err != nil {
			t.Fatal(err)
		}
	}
	export := func(n *placementNodeT, req *proto.ExportKeysRequest, ctx context.Context, failAt int) error {
		return n.s.replServer.Load().ExportKeys(req, &fakeServerStream[proto.ExportKeysChunk]{ctx: ctx, failAt: failAt})
	}
	refusals := []struct {
		name string
		node *placementNodeT
		req  *proto.ExportKeysRequest
		ctx  context.Context
	}{
		{"unauthorised", g2, &proto.ExportKeysRequest{Collection: c}, context.Background()},
		{"settings not from the meta leader", g2, &proto.ExportKeysRequest{Global: true}, peerCtx()},
		{"no collection", g2, &proto.ExportKeysRequest{}, peerCtx()},
		{"a follower", follower, &proto.ExportKeysRequest{Collection: c}, peerCtx()},
		{"another group's", meta, &proto.ExportKeysRequest{Collection: c}, peerCtx()},
	}
	for _, r := range refusals {
		if err := export(r.node, r.req, r.ctx, -1); err == nil {
			t.Errorf("%s: exported", r.name)
		}
	}
	// The stream breaks in the copy, after it, and in the log's tail.
	for _, failAt := range []int{0, 1, 2, 3} {
		if err := export(g2, &proto.ExportKeysRequest{Collection: c}, peerCtx(), failAt); err == nil {
			t.Errorf("a stream failing at message %d did not end the export", failAt)
		}
	}
	addKeysNotToCopy(t, g2, c)
	stream := &fakeServerStream[proto.ExportKeysChunk]{ctx: peerCtx(), failAt: -1}
	if err := g2.s.placement.copyKeys(&proto.ExportKeysRequest{Collection: c}, chunkSender{stream}); err != nil {
		t.Fatal(err)
	}
	// The copy took len(stream.sent) messages; the next, its end, fails.
	if err := export(g2, &proto.ExportKeysRequest{Collection: c}, peerCtx(), len(stream.sent)); err == nil {
		t.Fatal("the end of the copy was not sent, and the export went on")
	}
	if err := g2.s.placement.tailCollection(&fakeServerStream[proto.ExportKeysChunk]{ctx: peerCtx(), failAt: 0}, c, 0); err == nil {
		t.Fatal("a tail whose stream fails went on")
	}
}

// addKeysNotToCopy gives c's file a nested bucket and the node another
// collection's settings: neither is part of c's copy.
func addKeysNotToCopy(t *testing.T, n *placementNodeT, c string) {
	t.Helper()
	if err := n.s.collectionStore().get(c).Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("docs"))
		if err == nil {
			_, err = b.CreateBucketIfNotExists([]byte("nested"))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := logEntries(n.s.DBUpdate, n.s.Binlog, []*binlog.BinlogEntry{
		{Type: binlog.BinlogPut, BucketName: "schemas", Key: []byte("schema|someone-else"), Value: []byte("{}")}}); err != nil {
		t.Fatal(err)
	}
}

func TestPlacementExportSinkReportsProgressSparingly(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	n := tp.nodes[0]
	for i := range 3 {
		if err := writeDoc(n.s, "other", i); err != nil {
			t.Fatal(err)
		}
	}
	stream := &fakeServerStream[proto.ExportKeysChunk]{ctx: peerCtx(), failAt: -1}
	sink := &exportSink{stream: stream, collection: "mine", log: n.s.Binlog}
	other := func(lsn uint64) *proto.BinlogEntryProto {
		return &proto.BinlogEntryProto{Lsn: lsn, Type: uint32(binlog.BinlogPut), BucketName: "docs", Key: []byte("doc|other|x")}
	}
	_ = sink.Send(other(1)) // the first progress is reported
	_ = sink.Send(other(2)) // within the interval, and not the last entry: not
	_ = sink.Send(&proto.BinlogEntryProto{Lsn: 3, Type: uint32(binlog.BinlogDeleteBucket), BucketName: "docs"})
	if len(stream.sent) != 2 || len(stream.sent[1].Entries) != 1 {
		t.Fatalf("sent %d messages", len(stream.sent))
	}
}

func TestPlacementImportRefusalsAndFailures(t *testing.T) {
	tp, g2, follower := groupsOfTwo(t)
	meta := tp.leader("meta")
	c := nameIn(t, meta.s.placement, "meta")
	if err := writeDoc(meta.s, c, 1); err != nil {
		t.Fatal(err)
	}
	importInto := func(n *placementNodeT, ctx context.Context) error {
		return n.s.replServer.Load().ImportCollection(&proto.ImportCollectionRequest{Collection: c, SourceGroup: "meta"},
			&fakeServerStream[proto.ImportProgress]{ctx: ctx, failAt: -1})
	}
	if err := importInto(g2, context.Background()); err == nil {
		t.Fatal("an unauthorised import ran")
	}
	if err := importInto(follower, peerCtx()); err == nil {
		t.Fatal("a follower imported")
	}
	if err := importInto(g2, peerCtx()); err == nil || !strings.Contains(err.Error(), "no move") {
		t.Fatalf("an import no move asked for: %v", err)
	}
	p := g2.s.placement
	p.adopt(withEntry(p.current(), c, placementEntry{Group: "meta", MovingTo: "g2"}))
	req := &proto.ImportCollectionRequest{Collection: c, SourceGroup: "meta"}
	report := func(*proto.ImportProgress) error { return nil }
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	failures := []struct {
		name   string
		ctx    context.Context
		req    *proto.ImportCollectionRequest
		report func(*proto.ImportProgress) error
	}{
		{"a source without a leader", context.Background(), &proto.ImportCollectionRequest{Collection: c, SourceGroup: "nowhere"}, report},
		{"a caller that left", cancelled, req, report},
		{"a caller that cannot be told", context.Background(), req, func(*proto.ImportProgress) error { return errors.New("gone") }},
		// The meta group does not hold this one, and refuses it.
		{"what the source does not hold", context.Background(), &proto.ImportCollectionRequest{Collection: nameIn(t, p, "g2"), SourceGroup: "meta"}, report},
	}
	for _, f := range failures {
		if err := p.importCollection(f.ctx, f.req, f.report); err == nil {
			t.Errorf("%s: imported", f.name)
		}
	}
	// The source removes a whole collection bucket while it is copied.
	if err := importUntilBucketRemoved(p, meta, req); !errors.Is(err, errBucketDeleted) {
		t.Fatalf("a bucket removed at the source: %v", err)
	}
	// A copy left here that cannot be read stops the import before it starts.
	db, err := g2.s.collectionStore().getOrCreate(c)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := p.importCollection(context.Background(), req, report); err == nil {
		t.Fatal("imported over a copy that cannot be dropped")
	}
}

// importUntilBucketRemoved imports req and, once the copy is done, has the
// source log a whole collection bucket removed.
func importUntilBucketRemoved(p *placementRuntime, src *placementNodeT, req *proto.ImportCollectionRequest) error {
	copied := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- p.importCollection(context.Background(), req, func(pr *proto.ImportProgress) error {
			if pr.SnapshotDone {
				select {
				case copied <- struct{}{}:
				default:
				}
			}
			return nil
		})
	}()
	<-copied
	if err := src.s.Binlog.AppendBatch([]*binlog.BinlogEntry{{Type: binlog.BinlogDeleteBucket, BucketName: "docs"}}); err != nil {
		return err
	}
	return <-done
}

// withEntry is t with c placed as e.
func withEntry(t *placementTable, c string, e placementEntry) *placementTable {
	next := t.clone()
	next.Entries[c] = e
	next.Version++
	return next
}

func TestPlacementApplyCopiedRefusals(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 1}, "meta")
	p := tp.leader("meta").s.placement
	theirs := nameIn(t, p, "g2")
	put := func(bucket, key string) *proto.BinlogEntryProto {
		return &proto.BinlogEntryProto{Type: uint32(binlog.BinlogPut), BucketName: bucket, Key: []byte(key), Value: []byte("v")}
	}
	cases := []struct {
		name    string
		entries []*proto.BinlogEntryProto
	}{
		{"a bucket removed", []*proto.BinlogEntryProto{{Type: uint32(binlog.BinlogDeleteBucket), BucketName: "docs"}}},
		{"another group's file", []*proto.BinlogEntryProto{put("docs", "doc|"+theirs+"|k")}},
		{"another group's", []*proto.BinlogEntryProto{put("colmeta", theirs)}},
		{"an unknown entry type", []*proto.BinlogEntryProto{{Type: 99, BucketName: "colmeta", Key: []byte(theirs)}}},
	}
	for _, c := range cases {
		if err := p.applyCopied(theirs, c.entries); err == nil {
			t.Errorf("%s: applied", c.name)
		}
	}
	// A refused copy leaves no file: the placement loop would record one
	// as this group's, and the group would then take the collection's writes.
	if slices.Contains(p.s.collectionStore().names(), theirs) {
		t.Errorf("a refused copy left a file of %s", theirs)
	}
	// Geo points are loaded into the in-memory indexes when the import ends.
	s := p.s
	s.GeoStore, s.GeoIndex, s.GeoHashIndex = geo.NewGeoStore(s.DB), geo.NewGeoIndex(), geo.NewGeoHashIndex()
	s.GeoStore.SetRouter(s.router())
	_ = s.GeoStore.EnsureBucket()
	p.rebuildGeo(theirs)
}

func TestPlacementDropFailuresAndCaches(t *testing.T) {
	tp, g2, follower := groupsOfTwo(t)
	p := g2.s.placement
	c := nameIn(t, p, "g2")
	if err := writeDoc(g2.s, c, 1); err != nil {
		t.Fatal(err)
	}
	if err := logEntries(g2.s.DBUpdate, g2.s.Binlog, []*binlog.BinlogEntry{
		{Type: binlog.BinlogPut, BucketName: "schemas", Key: []byte("schema|" + c), Value: []byte("{}")}}); err != nil {
		t.Fatal(err)
	}
	waitE2E(t, "the follower to hold the collection and its settings", func() bool {
		_, n := follower.s.collectionChecksum(c)
		settings := 0
		_ = scanBuckets(follower.s.DB, settingsBucketNames(), ownedBy(c), func(string, []byte, []byte) error {
			settings++
			return nil
		})
		return n == 1 && settings == 1
	})
	// A follower's log takes no write: the drop fails, file first.
	if err := follower.s.placement.dropCopy(c); err == nil {
		t.Fatal("a follower dropped its copy")
	}
	fs := follower.s
	if err := fs.collectionStore().remove(c); err != nil {
		t.Fatal(err)
	}
	if err := follower.s.placement.dropCopy(c); err == nil {
		t.Fatal("a follower dropped its copy's settings")
	}
	// The leader drops it, and clears what it holds in memory.
	s := g2.s
	s.GeoIndex, s.GeoHashIndex = geo.NewGeoIndex(), geo.NewGeoHashIndex()
	s.LockFreeCache, s.SearchCache = cache.NewLockFreeCache(10, 60), newSearchCache()
	if err := p.dropCopy(c); err != nil {
		t.Fatal(err)
	}
	// A file that cannot be read stops a drop, and dropStale says so.
	d := ""
	for i := 0; d == ""; i++ {
		if g, _ := p.owner(fmt.Sprintf("closed-%d", i)); g == "g2" {
			d = fmt.Sprintf("closed-%d", i)
		}
	}
	if err := writeDoc(s, d, 1); err != nil {
		t.Fatal(err)
	}
	_ = s.collectionStore().get(d).Close()
	if err := p.dropCopy(d); err == nil {
		t.Fatal("dropped a file that cannot be read")
	}
	stale := unitRuntime(g2, nil, map[string]string{d: "meta"})
	stale.cfg.dropGrace = 0
	seen := map[string]time.Time{d: time.Now().Add(-time.Hour)}
	stale.dropStale(seen)
	if _, still := seen[d]; !still {
		t.Fatal("a drop that failed was forgotten")
	}
	_ = tp
}

func TestPlacementGlobalSettingsCopy(t *testing.T) {
	tp, g2, follower := groupsOfTwo(t)
	meta := tp.leader("meta")
	for _, n := range []*placementNodeT{meta, g2, follower} {
		n.s.AuthManager = NewAuthManager(n.s.DB, AuthConfig{})
		n.s.Tenants = NewTenantManager(n.s.DB)
	}
	globals := []*binlog.BinlogEntry{
		{Type: binlog.BinlogPut, BucketName: "auth_users", Key: []byte("u1"), Value: []byte(`{"username":"u1"}`)},
		{Type: binlog.BinlogPut, BucketName: "auth_users", Key: []byte("u2"), Value: []byte(`{"username":"u2"}`)},
		{Type: binlog.BinlogPut, BucketName: "tenants", Key: []byte("t1"), Value: []byte(`{}`)},
	}
	if err := logEntries(meta.s.DBUpdate, meta.s.Binlog, globals); err != nil {
		t.Fatal(err)
	}
	// g2 holds a key the meta group does not: it goes.
	if err := logEntries(g2.s.DBUpdate, g2.s.Binlog, []*binlog.BinlogEntry{
		{Type: binlog.BinlogPut, BucketName: "webhooks", Key: []byte("stray"), Value: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	if err := g2.s.placement.syncGlobal(); err != nil {
		t.Fatal(err)
	}
	if err := g2.s.placement.syncGlobal(); err != nil { // nothing differs now
		t.Fatal(err)
	}
	got := map[string]bool{}
	_ = scanBuckets(g2.s.DB, globalBuckets, keepAll, func(b string, k, _ []byte) error {
		got[b+"/"+string(k)] = true
		return nil
	})
	if !got["auth_users/u1"] || !got["tenants/t1"] || got["webhooks/stray"] {
		t.Fatalf("g2's global settings: %v", got)
	}
	// A follower's log takes nothing: the copy fails once it differs.
	if err := logEntries(meta.s.DBUpdate, meta.s.Binlog, []*binlog.BinlogEntry{
		{Type: binlog.BinlogPut, BucketName: "auth_users", Key: []byte("u3"), Value: []byte("{}")}}); err != nil {
		t.Fatal(err)
	}
	waitE2E(t, "the follower to hold u2", func() bool {
		found := false
		_ = scanBuckets(follower.s.DB, []string{"auth_users"}, keepAll, func(_ string, k, _ []byte) error {
			found = found || string(k) == "u2"
			return nil
		})
		return found
	})
	if err := follower.s.placement.syncGlobal(); err == nil {
		t.Fatal("a follower copied the global settings")
	}
}

// emptyExporter is a meta leader whose copy of the settings ends at once.
type emptyExporter struct{ scriptedLeader }

func (*emptyExporter) ExportKeys(*proto.ExportKeysRequest, proto.MDDBReplication_ExportKeysServer) error {
	return nil
}

func TestPlacementGlobalCopyFailures(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	p := unitRuntime(tp.nodes[0], nil, nil)
	p.cfg.self = "g2" // as if this node led another group
	p.cfg.groups["meta"] = []placementNode{{ID: "gone", GRPC: strings.TrimPrefix(closedURL(t), "http://"), HTTP: "http://gone.test"}}
	if err := p.syncGlobal(); err == nil {
		t.Fatal("copied from a meta group without a leader")
	}
	ex := &emptyExporter{scriptedLeader{id: "early"}}
	p.cfg.groups["meta"] = serveRepl(t, "early", ex)
	p.leaders.forget("meta")
	if err := p.syncGlobal(); err == nil || !strings.Contains(err.Error(), "ended early") {
		t.Fatalf("a copy that ends early: %v", err)
	}
	p.cfg.groups["meta"] = serveScripted(t, &scriptedLeader{id: "unimplemented"})
	p.leaders.forget("meta")
	if err := p.syncGlobal(); err == nil {
		t.Fatal("copied from a leader that does not export")
	}
}
