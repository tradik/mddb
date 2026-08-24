package main

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// shortSocketDir returns a short temp directory suitable for Unix Domain
// Socket paths. macOS caps `sun_path` at 104 bytes, and `t.TempDir()` on
// macOS returns `/var/folders/...` which is ~80+ chars before adding the
// test name, easily blowing the budget. Falling through to /tmp keeps the
// path short on every supported platform.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mddb-uds-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestParseListenAddr(t *testing.T) {
	cases := []struct {
		in       string
		wantNet  string
		wantAddr string
	}{
		{":11023", "tcp", ":11023"},
		{"localhost:11023", "tcp", "localhost:11023"},
		{"0.0.0.0:11023", "tcp", "0.0.0.0:11023"},
		{"unix:/tmp/mddb.sock", "unix", "/tmp/mddb.sock"},
		{"unix:///tmp/mddb.sock", "unix", "/tmp/mddb.sock"},
		{"unix:/var/run/mddb/http.sock", "unix", "/var/run/mddb/http.sock"},
	}
	for _, c := range cases {
		gotNet, gotAddr := parseListenAddr(c.in)
		if gotNet != c.wantNet || gotAddr != c.wantAddr {
			t.Errorf("parseListenAddr(%q) = (%q, %q); want (%q, %q)",
				c.in, gotNet, gotAddr, c.wantNet, c.wantAddr)
		}
	}
}

func TestIsUnixAddr(t *testing.T) {
	if !isUnixAddr("unix:/tmp/x.sock") {
		t.Error("isUnixAddr(unix:/tmp/x.sock) should be true")
	}
	if isUnixAddr(":11023") {
		t.Error("isUnixAddr(:11023) should be false")
	}
	if isUnixAddr("localhost:11023") {
		t.Error("isUnixAddr(localhost:11023) should be false")
	}
}

// requireUnixSockets skips a test that needs a working UDS listener.
//
// Not a blanket "skip on Windows": MDDB refuses to serve on a unix socket
// there on purpose, because the listener's security model is owner-only 0600
// mode bits and os.Chmod on Windows cannot express them (WIN-002). The refusal
// itself is asserted in listen_addr_windows_test.go, so what is skipped here is
// covered there — from the other side.
func requireUnixSockets(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("MDDB refuses unix socket listeners on Windows by design; " +
			"the refusal is asserted in TestOpenListenerUDSRefusedOnWindows")
	}
}

func TestOpenListenerUDS(t *testing.T) {
	requireUnixSockets(t)
	dir := shortSocketDir(t)
	path := filepath.Join(dir, "test.sock")
	lis, err := openListener("unix:" + path)
	if err != nil {
		t.Fatalf("openListener failed: %v", err)
	}
	defer func() { _ = closeListener(lis, "unix:"+path) }()

	if lis.Addr().Network() != "unix" {
		t.Errorf("Addr().Network() = %q; want unix", lis.Addr().Network())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("socket file missing: %v", err)
	}
	// Require owner-only permissions (0600). Mode bits after Type mask.
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("socket permissions = %o; want 0600", perm)
	}
}

func TestOpenListenerUDSStale(t *testing.T) {
	requireUnixSockets(t)
	dir := shortSocketDir(t)
	path := filepath.Join(dir, "stale.sock")
	// Create a stale file first — openListener should remove it before binding.
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}
	lis, err := openListener("unix:" + path)
	if err != nil {
		t.Fatalf("openListener should have cleaned stale socket: %v", err)
	}
	_ = closeListener(lis, "unix:"+path)
}

func TestCloseListenerRemovesSocket(t *testing.T) {
	requireUnixSockets(t)
	dir := shortSocketDir(t)
	path := filepath.Join(dir, "cleanup.sock")
	lis, err := openListener("unix:" + path)
	if err != nil {
		t.Fatalf("openListener: %v", err)
	}
	if err := closeListener(lis, "unix:"+path); err != nil {
		t.Fatalf("closeListener: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("socket still exists after closeListener: err=%v", err)
	}
}

func TestOpenListenerTCP(t *testing.T) {
	// Bind to an ephemeral port to avoid collisions.
	lis, err := openListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("openListener tcp: %v", err)
	}
	defer func() { _ = lis.Close() }()
	if lis.Addr().Network() != "tcp" {
		t.Errorf("Addr().Network() = %q; want tcp", lis.Addr().Network())
	}
	// Sanity: verify we can actually dial it.
	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("dial tcp listener: %v", err)
	}
	_ = conn.Close()
}
