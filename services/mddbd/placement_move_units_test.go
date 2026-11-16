package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	proto "mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GO-052: the steps of a move, against groups whose leaders are scripted.

// scriptedLeader is the replication service of a group's leader whose
// answers a test decides.
type scriptedLeader struct {
	proto.UnimplementedMDDBReplicationServer
	id     string
	lsn    atomic.Uint64
	probes chan uint64 // each probe's LSN, when set
	// importer runs an import; nil refuses it.
	importer func(stream proto.MDDBReplication_ImportCollectionServer) error
	fenceErr error
	through  uint64
	pushErr  error
}

func (l *scriptedLeader) ReplicationStatus(context.Context, *proto.ReplicationStatusRequest) (*proto.ReplicationStatusResponse, error) {
	lsn := l.lsn.Add(1)
	if l.probes != nil {
		select {
		case l.probes <- lsn:
		default:
		}
	}
	return &proto.ReplicationStatusResponse{NodeId: l.id, Role: "leader", LeaderId: l.id, CurrentLsn: lsn}, nil
}

func (l *scriptedLeader) ImportCollection(_ *proto.ImportCollectionRequest, stream proto.MDDBReplication_ImportCollectionServer) error {
	if l.importer == nil {
		return status.Error(codes.FailedPrecondition, "refused")
	}
	return l.importer(stream)
}

func (l *scriptedLeader) FenceCollection(context.Context, *proto.FenceCollectionRequest) (*proto.FenceCollectionResponse, error) {
	return &proto.FenceCollectionResponse{ThroughLsn: l.through}, l.fenceErr
}

func (l *scriptedLeader) PushPlacement(context.Context, *proto.PlacementMap) (*proto.PushPlacementResponse, error) {
	return &proto.PushPlacementResponse{}, l.pushErr
}

// serveScripted starts l and returns its group of one node.
func serveScripted(t *testing.T, l *scriptedLeader) []placementNode {
	t.Helper()
	return serveRepl(t, l.id, l)
}

// serveRepl serves a replication service as node id, a group of one.
func serveRepl(t *testing.T, id string, l proto.MDDBReplicationServer) []placementNode {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	proto.RegisterMDDBReplicationServer(srv, l)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return []placementNode{{ID: id, GRPC: lis.Addr().String(), HTTP: "http://" + id + ".test"}}
}

// sendProgress reports one step of an import.
func sendProgress(stream proto.MDDBReplication_ImportCollectionServer, done bool, applied uint64) error {
	return stream.Send(&proto.ImportProgress{SnapshotDone: done, AppliedLsn: applied, SourceLsn: applied})
}

// holdOpen keeps an import running until the caller ends it.
func holdOpen(stream proto.MDDBReplication_ImportCollectionServer) error {
	<-stream.Context().Done()
	return nil
}

// moveRig is a meta leader's runtime with scripted groups src and dst.
func moveRig(t *testing.T, src, dst *scriptedLeader) *placementRuntime {
	t.Helper()
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	return unitRuntime(tp.nodes[0], map[string][]placementNode{"src": serveScripted(t, src), "dst": serveScripted(t, dst)}, nil)
}

func shortCatchUp(t *testing.T) {
	prev := catchUpTimeout
	catchUpTimeout = 100 * time.Millisecond
	t.Cleanup(func() { catchUpTimeout = prev })
}

func TestPlacementMoveStepsThatFail(t *testing.T) {
	shortCatchUp(t)
	snapshotThenStall := func(stream proto.MDDBReplication_ImportCollectionServer) error {
		_ = sendProgress(stream, true, 0)
		return holdOpen(stream)
	}
	cases := []struct {
		name string
		src  *scriptedLeader
		dst  *scriptedLeader
		want string
	}{
		{"import refused", &scriptedLeader{id: "s"}, &scriptedLeader{id: "d"}, "import ended"},
		{"target stalls", &scriptedLeader{id: "s"}, &scriptedLeader{id: "d", importer: snapshotThenStall}, "did not catch up"},
		{"fence refused", &scriptedLeader{id: "s", fenceErr: errors.New("busy")}, &scriptedLeader{id: "d",
			importer: func(stream proto.MDDBReplication_ImportCollectionServer) error {
				_ = sendProgress(stream, true, 1<<40)
				return holdOpen(stream)
			}}, "busy"},
		{"target behind the fence", &scriptedLeader{id: "s", through: 1 << 41}, &scriptedLeader{id: "d",
			importer: func(stream proto.MDDBReplication_ImportCollectionServer) error {
				_ = sendProgress(stream, true, 1<<40)
				return holdOpen(stream)
			}}, "did not catch up"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := moveRig(t, c.src, c.dst)
			m := p.moves.begin("c", "src", "dst")
			if err := p.copyAndSwitch(context.Background(), m); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

func TestPlacementMoveCatchUpRoundsRunOut(t *testing.T) {
	src := &scriptedLeader{id: "s", probes: make(chan uint64, 1), fenceErr: errors.New("stop here")}
	dst := &scriptedLeader{id: "d"}
	dst.importer = func(stream proto.MDDBReplication_ImportCollectionServer) error {
		_ = sendProgress(stream, true, 0)
		for {
			select {
			case lsn := <-src.probes:
				time.Sleep(catchUpQuick + 50*time.Millisecond) // every round is slow
				_ = sendProgress(stream, true, lsn)
			case <-stream.Context().Done():
				return nil
			}
		}
	}
	p := moveRig(t, src, dst)
	m := p.moves.begin("c", "src", "dst")
	if err := p.copyAndSwitch(context.Background(), m); err == nil || !strings.Contains(err.Error(), "stop here") {
		t.Fatalf("after the rounds the source is fenced: %v", err)
	}
}

func TestPlacementMoveSourceWithoutLeader(t *testing.T) {
	dst := &scriptedLeader{id: "d", importer: func(stream proto.MDDBReplication_ImportCollectionServer) error {
		_ = sendProgress(stream, true, 0)
		return holdOpen(stream)
	}}
	p := moveRig(t, &scriptedLeader{id: "s"}, dst)
	// The source's leader is remembered at an address nothing answers.
	p.cfg.groups["src"] = []placementNode{{ID: "s", GRPC: strings.TrimPrefix(closedURL(t), "http://"), HTTP: "http://s.test"}}
	p.leaders.set("src", "s")
	m := p.moves.begin("c", "src", "dst")
	if err := p.copyAndSwitch(context.Background(), m); err == nil {
		t.Fatal("a source that cannot be probed was caught up with")
	}
	p.leaders.forget("src")
	if _, err := p.groupLSN("src"); err == nil || !strings.Contains(err.Error(), "no known leader") {
		t.Fatalf("no leader: %v", err)
	}
	if _, err := p.fenceSource(moveStatus{From: "src", Collection: "c"}); err == nil {
		t.Fatal("fenced a group without a leader")
	}
	if err := p.copyAndSwitch(context.Background(), p.moves.begin("d", "src", "nowhere")); err == nil {
		t.Fatal("imported into a group without a leader")
	}
	p.pushMap("nowhere") // no leader: skipped
}

func TestPlacementMoveSwitchFailsWhenTheMapCannotBeWritten(t *testing.T) {
	src := &scriptedLeader{id: "s", through: 5}
	dst := &scriptedLeader{id: "d", importer: func(stream proto.MDDBReplication_ImportCollectionServer) error {
		_ = sendProgress(stream, true, 1<<40)
		return holdOpen(stream)
	}}
	p := moveRig(t, src, dst)
	writeStoredMap(t, p, `{corrupt`)
	m := p.moves.begin("c", "src", "dst")
	if err := p.copyAndSwitch(context.Background(), m); err == nil {
		t.Fatal("switched without a map")
	}
	p.abandonMove("c") // the map cannot be written: logged, nothing else
	// startMove records the failure when the map cannot be marked.
	if m, err := p.startMove("c2", "dst"); err != nil || p.moves.get(m).Phase != movePhaseFailed {
		t.Fatalf("start over a corrupt map: %v", err)
	}
	writeStoredMap(t, p, `{"version":1,"term":1}`)
	p.moves.begin("c", "src", "dst")
	if _, err := p.startMove("c", "dst"); !errors.Is(err, errMoveRunning) {
		t.Fatalf("a second move: %v", err)
	}
}

func TestPlacementMetaDutiesSealAndGiveUpOrphans(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 1}, "meta")
	meta := tp.leader("meta")
	p := unitRuntime(meta, nil, nil)
	// A stored map that cannot be read is sealed again: that fails, and
	// says so.
	writeStoredMap(t, p, `{corrupt`)
	p.metaDuties()
	writeStoredMap(t, p, `{"version":1,"term":1,"entries":{"orphan":{"group":"meta","movingTo":"g2"}}}`)
	p.table.Store(p.storedMap())
	p.metaDuties()
	if e := p.storedMap().Entries["orphan"]; e.MovingTo != "" || e.Group != "meta" {
		t.Fatalf("the orphaned move stays: %+v", e)
	}
	// A group's leader that cannot reach the meta leader pins nothing.
	g2 := unitRuntime(tp.leader("g2"), nil, nil)
	g2.cfg.groups["meta"] = []placementNode{{ID: "gone", GRPC: strings.TrimPrefix(closedURL(t), "http://"), HTTP: "http://gone.test"}}
	g2.leaders.forget("meta")
	if err := tp.leader("g2").add(nameIn(t, g2, "g2"), "k", "x"); err != nil {
		t.Fatal(err)
	}
	g2.table.Store(&placementTable{Entries: map[string]placementEntry{}})
	g2.pinLocal()
}

func TestPlacementSwitchWaitsForTheTargetOnlySoLong(t *testing.T) {
	p := moveRig(t, &scriptedLeader{id: "s"}, &scriptedLeader{id: "d", pushErr: errors.New("busy")})
	p.cfg.fenceTimeout = 30 * time.Millisecond
	if err := p.switchTo(moveStatus{Collection: "c", From: "src", To: "dst"}); err != nil {
		t.Fatal(err)
	}
	if g, _ := p.owner("c"); g != "dst" {
		t.Fatalf("after a switch the target could not be told of: %s", g)
	}
}

func TestPlacementAwaitLeaseGivesUp(t *testing.T) {
	_, _, follower := groupsOfTwo(t)
	p := unitRuntime(follower, nil, nil)
	p.cfg.fenceTimeout = 20 * time.Millisecond
	began := time.Now()
	p.awaitLease(context.Background())
	if time.Since(began) < 20*time.Millisecond {
		t.Fatal("a node without a lease did not wait for one")
	}
}
