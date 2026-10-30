package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// installUpdate's remaining failure paths: no binary to find, and a binary
// that cannot be replaced. Both must end in an error, not a claimed update.

// newerReleaseServer serves v2.12.0 with a checksummed archive for it.
func newerReleaseServer(t *testing.T) {
	t.Helper()
	archive := tarballFor(t, "new binary")
	sum := sha256.Sum256(archive)
	name := artifactName("mddb-cli", "v2.12.0")
	withReleaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			_, _ = w.Write([]byte(`{"tag_name":"v2.12.0"}`))
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		default:
			_, _ = w.Write(archive)
		}
	})
}

// runOutdatedSelfUpdate runs self-update as an older plain binary.
func runOutdatedSelfUpdate(t *testing.T) error {
	t.Helper()
	original := Version
	t.Cleanup(func() { Version = original })
	Version = "v2.11.0"
	t.Setenv("SNAP", "")
	t.Setenv("SNAP_NAME", "")
	newerReleaseServer(t)
	cmd := newSelfUpdateCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs(nil)
	return cmd.Execute()
}

func TestSelfUpdateStopsWhenTheBinaryCannotBeFound(t *testing.T) {
	original := executablePath
	t.Cleanup(func() { executablePath = original })
	executablePath = func() (string, error) { return "", errors.New("no /proc") }

	err := runOutdatedSelfUpdate(t)
	if err == nil || !strings.Contains(err.Error(), "cannot locate this binary") {
		t.Fatalf("err = %v, want cannot locate this binary", err)
	}
}

func TestSelfUpdateStopsWhenTheBinaryCannotBeReplaced(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone", "mddb-cli")
	original := resolveExecutable
	t.Cleanup(func() { resolveExecutable = original })
	resolveExecutable = func(string) (string, error) { return missing, nil }

	if err := runOutdatedSelfUpdate(t); err == nil {
		t.Fatal("replacing a binary in a missing directory reported success")
	}
}
