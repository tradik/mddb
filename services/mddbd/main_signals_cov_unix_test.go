//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// Sending a signal to the own process needs syscall.Kill, which Windows
// does not have; the shutdown-signal wiring is the same there.
func TestCovANotifyShutdownSignals(t *testing.T) {
	ch := notifyShutdownSignals()
	defer signal.Stop(ch)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-ch:
		if sig != syscall.SIGTERM {
			t.Fatalf("got %v, want SIGTERM", sig)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM was not delivered to the shutdown channel")
	}
}
