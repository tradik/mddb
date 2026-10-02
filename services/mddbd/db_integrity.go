package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Proving a database file is one bbolt can open (#266, #270).
//
// mddb opens its database with NoFreelistSync, so bbolt rebuilds the freelist
// on every open by walking every page. A file whose page tree is damaged —
// a backup copied while writes were landing, or a database that went bad in
// place — fails that walk with a panic:
//
//	freepages: failed to get all reachable pages (page 119988: multiple references ...)
//
// and the panic cannot be contained in the process that hits it. bbolt's walk
// runs in a goroutine that keeps reading pages through a transaction the
// panicking side has already rolled back, so it can fault on its own, where no
// recover reaches; and when it does not, it is left blocked holding a read
// transaction, which is why `bbolt compact` on such a file hangs. A restore
// that opened a damaged backup in the server process therefore did not fail —
// it left the server with its database closed and the damaged file in its
// place.
//
// So a file is proven in a child process: the server re-runs its own binary,
// which opens the file the way the server would and runs bbolt's full check.
// Whatever bbolt does to that process, the server reads an exit code.

// verifyChildEnv names the file a child process should verify. Set only by
// verifyDatabase; the child is this binary (or the test binary, whose TestMain
// honours it too).
const verifyChildEnv = "MDDB_INTERNAL_VERIFY_DB"

// verifyTimeout bounds a check. Large databases on slow disks are slow to
// walk — tens of minutes have been reported cold on spinning disks — so the
// default is generous and MDDB_VERIFY_TIMEOUT can raise it.
func verifyTimeout() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("MDDB_VERIFY_TIMEOUT")); err == nil && d > 0 {
		return d
	}
	return time.Hour
}

// verifyDatabase reports whether the file at path passes bbolt's integrity
// check, run in a child process. The error carries bbolt's own description of
// the damage.
func verifyDatabase(path string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot run the integrity check: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout())
	defer cancel()

	// #nosec G204 -- runs this binary, with the path passed in the environment
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), verifyChildEnv+"="+path)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("%s: integrity check did not finish within %s (MDDB_VERIFY_TIMEOUT)", filepath.Base(path), verifyTimeout())
	}
	if err != nil {
		return fmt.Errorf("%s fails bbolt's integrity check: %s", filepath.Base(path), failureReason(string(out)))
	}
	return nil
}

// runVerifyChild is the child side: open the file as the server would, run
// the full check, report, and exit non-zero on any damage. A panic in here is
// also a failure — the parent sees the exit status and the panic text.
func runVerifyChild(path string) int {
	db, err := openBolt(path, &bolt.Options{
		ReadOnly:        true,
		PreLoadFreelist: true, // the walk every server open performs
		FreelistType:    bolt.FreelistMapType,
		Timeout:         5 * time.Second,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = db.Close() }()

	var problems []string
	_ = db.View(func(tx *bolt.Tx) error {
		// Drained to the end: bbolt's checker blocks if its channel is left.
		for e := range tx.Check() {
			if len(problems) < 10 {
				problems = append(problems, e.Error())
			}
		}
		return nil
	})
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, strings.Join(problems, "\n"))
		return 1
	}
	fmt.Println("ok")
	return 0
}

// openBolt opens a database and turns a panic inside bbolt into an error, so a
// damaged file is reported rather than taking down the goroutine that opened
// it. See the note above for why this is a last line of defence, not a check.
func openBolt(path string, opts *bolt.Options) (db *bolt.DB, err error) {
	defer func() {
		if r := recover(); r != nil {
			db, err = nil, fmt.Errorf("bbolt panicked opening %s: %v", filepath.Base(path), r)
		}
	}()
	return bolt.Open(path, 0o600, opts)
}

// failureReason picks bbolt's own words out of a child's output: the panic
// message if it crashed, its report otherwise — not a stack trace.
func failureReason(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "panic: "); i >= 0 {
			return strings.TrimSpace(line[i+len("panic: "):])
		}
	}
	return lastLines(out, 6)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
