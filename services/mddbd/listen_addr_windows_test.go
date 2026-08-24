//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The other side of the skip in listen_addr_test.go: on Windows a unix socket
// listener must refuse to start, and say something a reader can act on.
//
// Worth asserting rather than assuming, because this is not a build failure.
// Windows 10 supports AF_UNIX and net.Listen("unix", …) succeeds — the listener
// would come up and serve, with the owner-only restriction it documents
// silently absent. The refusal is the feature.
func TestOpenListenerUDSRefusedOnWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sock")

	lis, err := openListener("unix:" + path)
	if err == nil {
		_ = closeListener(lis, "unix:"+path)
		t.Fatal("openListener created a unix socket on Windows, where its " +
			"owner-only guarantee cannot hold")
	}

	// The message has to tell whoever hit it what to do instead. A bare
	// "unsupported" leaves them guessing at a config file.
	if !strings.Contains(err.Error(), "tcp://127.0.0.1") {
		t.Errorf("error does not point at the alternative: %v", err)
	}
	if !strings.Contains(err.Error(), "owner") {
		t.Errorf("error does not say why it refused: %v", err)
	}
}
