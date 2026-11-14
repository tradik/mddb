package lspindex

import (
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// assertReaped fails when the server process still exists, running or as
// a zombie: Index must wait for it before returning.
func assertReaped(t *testing.T, pid int) {
	t.Helper()
	if runtime.GOOS != "linux" || pid == 0 {
		return
	}
	if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); err == nil {
		status, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		t.Errorf("server process %d outlived Index:\n%s", pid, status)
	}
}

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertNoGoroutineLeak fails when more goroutines run than before, after a
// grace period for ones that are finishing.
func assertNoGoroutineLeak(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines: %d before, %d after\n%s", before, runtime.NumGoroutine(), buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}
