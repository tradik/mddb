package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// OPS-019. Someone who downloaded a binary onto a VPS had no way to learn a
// newer one exists, so they stayed on the version they first installed —
// security fixes included.

func TestDetectInstallMethod(t *testing.T) {
	t.Run("plain binary", func(t *testing.T) {
		t.Setenv("SNAP", "")
		t.Setenv("SNAP_NAME", "")
		if got := DetectInstallMethod(); got != InstallBinary {
			t.Errorf("got %q, want %q", got, InstallBinary)
		}
	})

	// Replacing a file a package manager owns leaves its record disagreeing
	// with the disk, and the next refresh undoes the update anyway.
	t.Run("snap", func(t *testing.T) {
		t.Setenv("SNAP", "/snap/mddb/current")
		if got := DetectInstallMethod(); got != InstallSnap {
			t.Errorf("got %q, want %q", got, InstallSnap)
		}
	})

	t.Run("snap by name alone", func(t *testing.T) {
		t.Setenv("SNAP", "")
		t.Setenv("SNAP_NAME", "mddb")
		if got := DetectInstallMethod(); got != InstallSnap {
			t.Errorf("got %q, want %q", got, InstallSnap)
		}
	})
}

func TestUpdateInstructionsNameTheRightChannel(t *testing.T) {
	if got := UpdateInstructions(InstallSnap); !strings.Contains(got, "snap refresh") {
		t.Errorf("snap instructions do not mention snap refresh: %q", got)
	}
	if got := UpdateInstructions(InstallDocker); !strings.Contains(got, "docker pull") {
		t.Errorf("docker instructions do not mention docker pull: %q", got)
	}
	if got := UpdateInstructions(InstallBinary); got != "" {
		t.Errorf("a plain binary needs no instructions, got %q", got)
	}
}

func TestArtifactName(t *testing.T) {
	name := artifactName("mddb-cli", "v2.12.0")
	if !strings.HasPrefix(name, "mddb-cli-v2.12.0-") || !strings.HasSuffix(name, ".tar.gz") {
		t.Errorf("got %q", name)
	}
}

// The version reaches artifactURL from GitHub's API rather than from a user,
// and is still constrained: an update path that can be aimed is a way to
// install someone else's binary.
func TestArtifactURLRefusesAnythingButATag(t *testing.T) {
	for _, bad := range []string{
		"../../../etc",
		"v2.12.0/../../other",
		"v2.12.0?x=1",
		"v2.12.0#frag",
		"https://evil.example.com/",
		"",
		strings.Repeat("v", 100),
		"v2 12 0",
	} {
		if _, err := artifactURL(bad, "x.tar.gz"); err == nil {
			t.Errorf("%q was accepted as a version", bad)
		}
	}
}

func TestArtifactURLBuildsFromThePinnedBase(t *testing.T) {
	got, err := artifactURL("v2.12.0", "mddb-cli-v2.12.0-linux-amd64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "https://github.com/tradik/mddb/releases/download/") {
		t.Errorf("URL left the pinned base: %s", got)
	}
}

func TestIsTagShaped(t *testing.T) {
	for _, ok := range []string{"v2.12.0", "2.12.0", "v2.12.0-rc1", "v2.12.0+build.1"} {
		if !isTagShaped(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"", "a/b", "a b", "..", "v1..2", "a\nb"} {
		if isTagShaped(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}

// --- archive handling ------------------------------------------------------

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	archive := tarball(t, map[string]string{
		"mddb-cli-v2.12.0-linux-amd64/README.md": "readme",
		"mddb-cli-v2.12.0-linux-amd64/mddb-cli":  "the binary",
	})

	got, err := ExtractBinary(archive, "mddb-cli")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the binary" {
		t.Errorf("got %q", got)
	}
}

// The classic archive-extraction hole cannot apply here: only the basename is
// matched and no path from the archive is ever used as a destination.
func TestExtractBinaryIgnoresThePathInTheArchive(t *testing.T) {
	archive := tarball(t, map[string]string{
		"../../../etc/cron.d/mddb-cli": "malicious",
	})

	got, err := ExtractBinary(archive, "mddb-cli")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "malicious" {
		t.Fatalf("got %q", got)
	}
	// The point: the content is returned, and it is the caller that decides
	// where it goes — which is always the current binary's own path.
	if _, err := os.Stat("/etc/cron.d/mddb-cli"); err == nil {
		t.Fatal("extraction wrote to a path from the archive")
	}
}

func TestExtractBinaryOnAnArchiveWithoutIt(t *testing.T) {
	archive := tarball(t, map[string]string{"dist/README.md": "readme"})
	if _, err := ExtractBinary(archive, "mddb-cli"); err == nil {
		t.Error("a missing binary was not reported")
	}
}

func TestExtractBinaryOnGarbage(t *testing.T) {
	if _, err := ExtractBinary([]byte("not a gzip stream"), "mddb-cli"); err == nil {
		t.Error("garbage was accepted as an archive")
	}
}

// --- replacement -----------------------------------------------------------

func TestReplaceBinaryIsAtomicAndKeepsABackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mddb-cli")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil { // #nosec G306 -- an executable in a temp dir
		t.Fatal(err)
	}

	backup, err := ReplaceBinary(path, []byte("new binary"))
	if err != nil {
		t.Fatal(err)
	}

	installed, _ := os.ReadFile(path) // #nosec G304 -- a path this test created
	if string(installed) != "new binary" {
		t.Errorf("installed content is %q", installed)
	}
	kept, _ := os.ReadFile(backup) // #nosec G304 -- a path this test created
	if string(kept) != "old binary" {
		t.Errorf("backup content is %q", kept)
	}

	// Windows has no execute bit — what makes a file runnable there is its
	// extension, and Go synthesises Mode().Perm() from the read-only attribute
	// alone, so this can never hold. That the mode is carried over from the
	// binary being replaced is asserted by
	// TestReplaceBinaryPreservesTheExistingMode, which does run there.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Error("the installed binary is not executable")
		}
	}

	// No leftovers: an update that ran must not litter the directory it ran in.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".mddb-update-") {
			t.Errorf("a temporary file survived: %s", e.Name())
		}
	}
}

func TestReplaceBinaryRefusesAnUnwritableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		// os.Chmod on Windows toggles the read-only attribute and does not
		// take write access away from the owner, so the directory this test
		// makes unwritable stays writable and the update rightly succeeds.
		// Denying write access there needs an ACL, which is a different
		// subject from the one this test is about.
		t.Skip("Unix mode bits do not restrict the owner on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root, which can write anywhere")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mddb-cli")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil { // #nosec G306 -- an executable in a temp dir
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()

	_, err := ReplaceBinary(path, []byte("new"))
	if err == nil {
		t.Fatal("an unwritable directory was accepted")
	}
	// The message has to name the way out, or someone hits a permission error
	// with no next step.
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("the refusal does not suggest sudo: %v", err)
	}

	// The working binary is untouched.
	kept, _ := os.ReadFile(path) // #nosec G304 -- a path this test created
	if string(kept) != "old" {
		t.Errorf("the existing binary was damaged: %q", kept)
	}
}

// --- download and verification --------------------------------------------

func TestDownloadAndVerifyRejectsAChecksumMismatch(t *testing.T) {
	// A mismatch must stop before anything on disk is touched, and say so.
	content := []byte("the artifact")
	sum := sha256.Sum256([]byte("something else"))

	err := VerifyChecksum(content, hex.EncodeToString(sum[:]), "artifact.tar.gz")
	if err == nil {
		t.Fatal("a mismatched checksum was accepted")
	}
	if !strings.Contains(err.Error(), "nothing has been changed") {
		t.Errorf("the refusal does not say the binary is untouched: %v", err)
	}
}

func TestVerifyChecksumAcceptsAMatch(t *testing.T) {
	content := []byte("the artifact")
	sum := sha256.Sum256(content)

	if err := VerifyChecksum(content, hex.EncodeToString(sum[:]), "artifact.tar.gz"); err != nil {
		t.Errorf("a matching checksum was refused: %v", err)
	}
	// sha256sum writes lowercase, but a hand-edited checksums.txt might not.
	if err := VerifyChecksum(content, strings.ToUpper(hex.EncodeToString(sum[:])), "x"); err != nil {
		t.Errorf("an uppercase checksum was refused: %v", err)
	}
}

func TestParseChecksumLine(t *testing.T) {
	// The format sha256sum produces, including its binary-mode asterisk.
	body := "aa11  other.tar.gz\nbb22 *mddb-cli-v2.12.0-linux-amd64.tar.gz\n\ngarbage line\n"
	got, err := checksumFor(body, "mddb-cli-v2.12.0-linux-amd64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != "bb22" {
		t.Errorf("got %q, want bb22", got)
	}
	if _, err := checksumFor(body, "absent.tar.gz"); err == nil {
		t.Error("a missing entry was not reported")
	}
}

func TestFetchLatestReleaseIsCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FetchLatestRelease(ctx); err == nil {
		t.Error("a cancelled context did not stop the check")
	}
}

func TestDetectInstallMethodFindsAContainer(t *testing.T) {
	t.Setenv("SNAP", "")
	t.Setenv("SNAP_NAME", "")

	marker := filepath.Join(t.TempDir(), ".dockerenv")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	original := dockerMarkerPath
	dockerMarkerPath = marker
	defer func() { dockerMarkerPath = original }()

	if got := DetectInstallMethod(); got != InstallDocker {
		t.Errorf("got %q, want %q", got, InstallDocker)
	}
}

func TestRealpathResolvesASymlink(t *testing.T) {
	// os.Executable returning a symlink is common — a binary in /usr/local/bin
	// linked from ~/bin — and replacing the link instead of the target would be
	// a quiet no-op.
	dir := t.TempDir()
	target := filepath.Join(dir, "mddb-cli")
	link := filepath.Join(dir, "mddb-cli-link")
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil { // #nosec G306 -- an executable in a temp dir
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := realpath(link)
	if err != nil {
		t.Fatal(err)
	}
	resolvedTarget, _ := filepath.EvalSymlinks(target)
	if got != resolvedTarget {
		t.Errorf("got %q, want %q", got, resolvedTarget)
	}
}

func TestExtractBinarySkipsDirectoriesAndLinks(t *testing.T) {
	// A real release tarball has a directory entry before the binary, and the
	// loop must not mistake one for the file it is looking for.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "dist/mddb-cli", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: "dist/link-mddb-cli", Typeflag: tar.TypeSymlink, Linkname: "mddb-cli"})
	content := "the real binary"
	_ = tw.WriteHeader(&tar.Header{
		Name: "dist/mddb-cli", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content)),
	})
	_, _ = tw.Write([]byte(content))
	_ = tw.Close()
	_ = gz.Close()

	got, err := ExtractBinary(buf.Bytes(), "mddb-cli")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("got %q, want the regular file's content", got)
	}
}

func TestExtractBinaryOnATruncatedArchive(t *testing.T) {
	full := tarball(t, map[string]string{"dist/mddb-cli": "binary"})
	if _, err := ExtractBinary(full[:len(full)/2], "mddb-cli"); err == nil {
		t.Error("a truncated archive was accepted")
	}
}

func TestReplaceBinaryLeavesTheOriginalWhenTheTargetIsGone(t *testing.T) {
	// Rename of a path that does not exist: the temporary file must not be
	// left behind, and the error must say what failed.
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist")

	if _, err := ReplaceBinary(path, []byte("new")); err == nil {
		t.Fatal("replacing a missing binary reported success")
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".mddb-update-") {
			t.Errorf("a temporary file survived a failed replacement: %s", e.Name())
		}
	}
}

func TestFetchChecksumSurfacesAFetchFailure(t *testing.T) {
	// No server: the connection is refused, and the error should say it was
	// the checksums that could not be fetched.
	_, err := fetchChecksum(context.Background(), "http://127.0.0.1:1/checksums.txt", "x.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "fetching checksums") {
		t.Errorf("got %v, want a checksum-fetch error", err)
	}
}

func TestFetchBytesOnAnUnreachableHost(t *testing.T) {
	if _, err := fetchBytes(context.Background(), "http://127.0.0.1:1/"); err == nil {
		t.Error("an unreachable host was accepted")
	}
}

func TestFetchBytesOnAnInvalidURL(t *testing.T) {
	if _, err := fetchBytes(context.Background(), "://not-a-url"); err == nil {
		t.Error("an invalid URL was accepted")
	}
}

// The replacement inherits the mode of the binary it replaces. An operator who
// widened or narrowed the permissions had a reason to, and an updater that
// resets them to its own idea of correct silently undoes that.
func TestReplaceBinaryPreservesTheExistingMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o700, 0o775} {
		dir := t.TempDir()
		path := filepath.Join(dir, "mddb-cli")
		if err := os.WriteFile(path, []byte("old"), mode); err != nil { // #nosec G306 -- the mode under test
			t.Fatal(err)
		}
		// WriteFile is subject to umask, so read back what actually landed.
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := ReplaceBinary(path, []byte("new")); err != nil {
			t.Fatalf("mode %v: %v", mode, err)
		}

		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if after.Mode().Perm() != before.Mode().Perm() {
			t.Errorf("mode %v became %v", before.Mode().Perm(), after.Mode().Perm())
		}
	}
}
