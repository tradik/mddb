package main

import (
	"net"
	"os"
	"os/exec"
	"testing"
)

// A leader killed under write load (GO-050). The leader runs in a child
// process — this test binary again, in TestE2ELeaderChildProcess — so the
// kill is a real one: no deferred close, no flush, whatever was buffered is
// gone. After the restart the follower holds exactly what the leader holds.

const (
	envChildDir  = "MDDB_E2E_LEADER_DIR"
	envChildAddr = "MDDB_E2E_LEADER_ADDR"
)

// TestE2ELeaderChildProcess is the leader of TestE2ELeaderKilledUnderLoad;
// it does nothing when run on its own.
func TestE2ELeaderChildProcess(t *testing.T) {
	dir := os.Getenv(envChildDir)
	if dir == "" {
		return
	}
	l := startE2ELeader(t, e2eLeaderOpts{dir: dir, addr: os.Getenv(envChildAddr)})
	for i := 0; ; i++ { // until killed
		if err := writeDoc(l.s, "blog", i); err != nil {
			t.Fatal(err)
		}
	}
}

func TestE2ELeaderKilledUnderLoad(t *testing.T) {
	useSecret(t)
	dir := t.TempDir()
	addr := freeTCPAddr(t)
	child := exec.Command(os.Args[0], "-test.run=^TestE2ELeaderChildProcess$") // #nosec G204 G702 -- this test binary
	child.Env = append(os.Environ(), envChildDir+"="+dir, envChildAddr+"="+addr)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	killed := false
	defer func() {
		if !killed {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()

	f := startE2EFollower(t, e2eFollowerOpts{dir: t.TempDir(), leader: addr})
	defer f.stop()
	waitE2E(t, "the follower to replicate the writes", func() bool { return f.rc.applier.LastAppliedLSN() > 300 })
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	killed = true
	before := f.rc.applier.Position()

	l := startE2ELeader(t, e2eLeaderOpts{dir: dir, addr: addr})
	defer l.stop(t)
	if l.s.Binlog.LogID() == before.LogID {
		t.Fatal("the leader continued a log it did not close")
	}
	l.write(t, "wiki", 0, 10)
	waitE2E(t, "the follower to resynchronise", func() bool { return caughtUp(l, f) })
	if n := l.snapshots.Load(); n != 1 {
		t.Errorf("the follower took %d snapshots after the crash, want 1", n)
	}
	assertSameContent(t, l, f)
}

// freeTCPAddr is a loopback address with a port nothing listens on.
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()
	return addr
}
