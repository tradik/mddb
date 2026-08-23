package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// OPS-019: the network halves. The endpoints are package variables purely so
// these tests can reach them; nothing outside this package can.

func withReleaseServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	apiOriginal, baseOriginal := releaseAPIURL, releaseDownloadBase
	releaseAPIURL = server.URL + "/releases/latest"
	releaseDownloadBase = server.URL + "/download"
	t.Cleanup(func() { releaseAPIURL, releaseDownloadBase = apiOriginal, baseOriginal })

	return server
}

func TestFetchLatestRelease(t *testing.T) {
	withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v2.12.0","html_url":"https://example.test/r","published_at":"2026-08-23T00:00:00Z"}`))
	})

	release, err := FetchLatestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "v2.12.0" || release.URL != "https://example.test/r" {
		t.Errorf("got %+v", release)
	}
}

func TestFetchLatestReleaseRejectsWhatIsNotAReleasedVersion(t *testing.T) {
	cases := map[string]string{
		"draft":       `{"tag_name":"v2.13.0","draft":true}`,
		"pre-release": `{"tag_name":"v2.13.0-rc1","prerelease":true}`,
		"no tag":      `{"html_url":"https://example.test"}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			})
			if _, err := FetchLatestRelease(context.Background()); err == nil {
				t.Errorf("a %s was accepted as the latest release", name)
			}
		})
	}
}

// "403" on its own reads like a permissions problem; it is almost always the
// unauthenticated rate limit, and saying so saves the guesswork.
func TestFetchLatestReleaseNamesTheRateLimit(t *testing.T) {
	withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	_, err := FetchLatestRelease(context.Background())
	if err == nil || !strings.Contains(err.Error(), "rate-limited") {
		t.Errorf("got %v, want a rate-limit explanation", err)
	}
}

func TestFetchLatestReleaseOnAnErrorStatus(t *testing.T) {
	withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := FetchLatestRelease(context.Background()); err == nil {
		t.Error("a 500 was accepted")
	}
}

func TestFetchLatestReleaseOnGarbage(t *testing.T) {
	withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	})
	if _, err := FetchLatestRelease(context.Background()); err == nil {
		t.Error("HTML was accepted as release JSON")
	}
}

// --- download and verify ---------------------------------------------------

func releaseWithArtifact(t *testing.T, name string, content []byte, checksum string) {
	t.Helper()
	withReleaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			_, _ = fmt.Fprintf(w, "%s  %s\n", checksum, name)
		case strings.HasSuffix(r.URL.Path, name):
			_, _ = w.Write(content)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestDownloadAndVerifyAcceptsAMatchingArtifact(t *testing.T) {
	content := []byte("artifact bytes")
	sum := sha256.Sum256(content)
	name := "mddb-cli-v2.12.0-linux-amd64.tar.gz"
	releaseWithArtifact(t, name, content, hex.EncodeToString(sum[:]))

	got, err := DownloadAndVerify(context.Background(), "v2.12.0", name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Error("the returned bytes are not what the server sent")
	}
}

func TestDownloadAndVerifyRefusesAnAlteredArtifact(t *testing.T) {
	// What a corrupted download or a swapped file in transit looks like.
	name := "mddb-cli-v2.12.0-linux-amd64.tar.gz"
	sum := sha256.Sum256([]byte("what the release says"))
	releaseWithArtifact(t, name, []byte("what actually arrived"), hex.EncodeToString(sum[:]))

	_, err := DownloadAndVerify(context.Background(), "v2.12.0", name)
	if err == nil {
		t.Fatal("an artifact that does not match its checksum was accepted")
	}
	if !strings.Contains(err.Error(), "nothing has been changed") {
		t.Errorf("the refusal does not say the binary is untouched: %v", err)
	}
}

func TestDownloadAndVerifyWhenTheChecksumIsMissing(t *testing.T) {
	withReleaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			_, _ = w.Write([]byte("aa11  something-else.tar.gz\n"))
			return
		}
		_, _ = w.Write([]byte("artifact"))
	})

	_, err := DownloadAndVerify(context.Background(), "v2.12.0", "mddb-cli-v2.12.0-linux-amd64.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "no entry for") {
		t.Errorf("got %v, want a missing-entry error", err)
	}
}

func TestDownloadAndVerifyRefusesABadVersion(t *testing.T) {
	if _, err := DownloadAndVerify(context.Background(), "../etc", "x.tar.gz"); err == nil {
		t.Error("a traversal version reached the download")
	}
}

func TestFetchBytesRefusesAnOversizedBody(t *testing.T) {
	withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// One byte past the cap.
		_, _ = w.Write(make([]byte, maxArtifactBytes+1))
	})
	if _, err := fetchBytes(context.Background(), releaseDownloadBase); err == nil {
		t.Error("an oversized body was accepted")
	}
}

func TestFetchBytesOnANotFound(t *testing.T) {
	withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if _, err := fetchBytes(context.Background(), releaseDownloadBase+"/missing"); err == nil {
		t.Error("a 404 was accepted")
	}
}

// --- the command -----------------------------------------------------------

func runSelfUpdateCmd(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newSelfUpdateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("self-update: %v\n%s", err, out.String())
	}
	return out.String()
}

func TestSelfUpdateReportsADevelopmentBuild(t *testing.T) {
	original := Version
	defer func() { Version = original }()
	Version = devVersion

	withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v2.12.0"}`))
	})

	out := runSelfUpdateCmd(t, "--check")
	if !strings.Contains(out, "development build") {
		t.Errorf("got %q", out)
	}
}

func TestSelfUpdateSaysWhenUpToDate(t *testing.T) {
	original := Version
	defer func() { Version = original }()
	Version = "v2.12.0"

	withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v2.12.0"}`))
	})

	if out := runSelfUpdateCmd(t, "--check"); !strings.Contains(out, "up to date") {
		t.Errorf("got %q", out)
	}
}

// Reporting comes before refusing on purpose: someone on a snap still wants to
// know an update exists, they just need a different way to get it.
func TestSelfUpdateReportsBeforeRefusingOnASnap(t *testing.T) {
	original := Version
	defer func() { Version = original }()
	Version = "v2.11.0"
	t.Setenv("SNAP", "/snap/mddb/current")

	withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v2.12.0","html_url":"https://example.test/r"}`))
	})

	out := runSelfUpdateCmd(t)
	if !strings.Contains(out, "v2.12.0") {
		t.Errorf("the available version was not reported: %q", out)
	}
	if !strings.Contains(out, "snap refresh") {
		t.Errorf("the right channel was not named: %q", out)
	}
}

func TestSelfUpdateCheckStopsBeforeInstalling(t *testing.T) {
	original := Version
	defer func() { Version = original }()
	Version = "v2.11.0"
	t.Setenv("SNAP", "")
	t.Setenv("SNAP_NAME", "")

	withReleaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("--check downloaded %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"tag_name":"v2.12.0"}`))
	})

	out := runSelfUpdateCmd(t, "--check")
	if !strings.Contains(out, "self-update") {
		t.Errorf("the next step was not suggested: %q", out)
	}
}

func TestSelfUpdateInstallsEndToEnd(t *testing.T) {
	original := Version
	defer func() { Version = original }()
	Version = "v2.11.0"
	t.Setenv("SNAP", "")
	t.Setenv("SNAP_NAME", "")

	// A binary this test owns, so the replacement is real without touching the
	// test runner's own executable.
	dir := t.TempDir()
	path := filepath.Join(dir, "mddb-cli")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil { // #nosec G306 -- an executable in a temp dir
		t.Fatal(err)
	}
	originalResolve := resolveExecutable
	resolveExecutable = func(string) (string, error) { return path, nil }
	defer func() { resolveExecutable = originalResolve }()

	archive := tarballFor(t, "new binary")
	sum := sha256.Sum256(archive)
	name := artifactName("mddb-cli", "v2.12.0")

	withReleaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			_, _ = w.Write([]byte(`{"tag_name":"v2.12.0"}`))
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		case strings.HasSuffix(r.URL.Path, name):
			_, _ = w.Write(archive)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out := runSelfUpdateCmd(t)

	installed, _ := os.ReadFile(path) // #nosec G304 -- a path this test created
	if string(installed) != "new binary" {
		t.Errorf("the binary was not replaced, it holds %q", installed)
	}
	backup, _ := os.ReadFile(path + ".bak") // #nosec G304 -- a path this test created
	if string(backup) != "old binary" {
		t.Errorf("no usable backup was left, it holds %q", backup)
	}
	for _, want := range []string{"Checksum verified", "v2.11.0", "v2.12.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func tarballFor(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "mddb-cli-v2.12.0-linux-amd64/mddb-cli", Mode: 0o755,
		Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestSelfUpdateSurfacesACheckFailure(t *testing.T) {
	withReleaseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	cmd := newSelfUpdateCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err == nil {
		t.Error("a failed check reported success")
	}
}

var _ = cobra.Command{}

// installUpdate's failure paths. Each one must leave the existing binary
// working, which is the property the whole ordering exists to guarantee.
func TestSelfUpdateStopsOnAFailedDownload(t *testing.T) {
	original := Version
	defer func() { Version = original }()
	Version = "v2.11.0"
	t.Setenv("SNAP", "")
	t.Setenv("SNAP_NAME", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "mddb-cli")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil { // #nosec G306 -- an executable in a temp dir
		t.Fatal(err)
	}
	originalResolve := resolveExecutable
	resolveExecutable = func(string) (string, error) { return path, nil }
	defer func() { resolveExecutable = originalResolve }()

	name := artifactName("mddb-cli", "v2.12.0")
	withReleaseServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			_, _ = w.Write([]byte(`{"tag_name":"v2.12.0"}`))
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			// A checksum for something else entirely.
			_, _ = fmt.Fprintf(w, "%s  %s\n", strings.Repeat("0", 64), name)
		default:
			_, _ = w.Write([]byte("whatever arrived"))
		}
	})

	cmd := newSelfUpdateCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("a checksum mismatch did not stop the update")
	}

	kept, _ := os.ReadFile(path) // #nosec G304 -- a path this test created
	if string(kept) != "old binary" {
		t.Errorf("the working binary was touched despite the failure: %q", kept)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("a backup was created for an update that never ran")
	}
}

func TestSelfUpdateStopsWhenTheArchiveHasNoBinary(t *testing.T) {
	original := Version
	defer func() { Version = original }()
	Version = "v2.11.0"
	t.Setenv("SNAP", "")
	t.Setenv("SNAP_NAME", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "mddb-cli")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil { // #nosec G306 -- an executable in a temp dir
		t.Fatal(err)
	}
	originalResolve := resolveExecutable
	resolveExecutable = func(string) (string, error) { return path, nil }
	defer func() { resolveExecutable = originalResolve }()

	// A well-formed, correctly checksummed archive that simply does not
	// contain what we came for.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "dist/README.md", Typeflag: tar.TypeReg, Size: 6, Mode: 0o644})
	_, _ = tw.Write([]byte("readme"))
	_ = tw.Close()
	_ = gz.Close()

	archive := buf.Bytes()
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

	cmd := newSelfUpdateCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("an archive without the binary did not stop the update")
	}

	kept, _ := os.ReadFile(path) // #nosec G304 -- a path this test created
	if string(kept) != "old binary" {
		t.Errorf("the working binary was touched: %q", kept)
	}
}

// The default resolveExecutable, exercised so the wiring is not only tested
// through its stub.
func TestResolveExecutableDefaultFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("x"), 0o755); err != nil { // #nosec G306 -- an executable in a temp dir
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := resolveExecutable(link)
	if err != nil {
		t.Fatal(err)
	}
	resolvedTarget, _ := filepath.EvalSymlinks(target)
	if got != resolvedTarget {
		t.Errorf("got %q, want %q", got, resolvedTarget)
	}
}
