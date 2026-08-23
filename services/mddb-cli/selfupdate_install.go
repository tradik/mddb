package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Self-update: downloading, verifying and replacing the binary (OPS-019).
//
// The order is the point. Nothing touches the installed binary until the
// download has been read to the end and its checksum matched, so an interrupted
// or corrupted download leaves a working binary in place. The replacement
// itself is a rename within the same directory, which is atomic — there is no
// instant at which the path holds half a file.

const (
	downloadTimeout = 5 * time.Minute

	// maxArtifactBytes bounds what an update will read. The release tarballs
	// are a few megabytes; this is generous enough not to bind and small
	// enough that a wrong URL cannot fill a disk.
	maxArtifactBytes = 256 << 20
)

// UpdateResult describes what an update did.
type UpdateResult struct {
	FromVersion string
	ToVersion   string
	BinaryPath  string
	BackupPath  string
}

// DownloadAndVerify fetches an artifact and checks it against the release's
// checksums.txt, returning the verified bytes.
//
// The checksum comes from the same release as the artifact, which proves the
// download arrived intact. It does not prove the release is genuine — see the
// note in selfupdate.go.
func DownloadAndVerify(ctx context.Context, version, name string) ([]byte, error) {
	artifact, err := artifactURL(version, name)
	if err != nil {
		return nil, err
	}
	checksums, err := artifactURL(version, "checksums.txt")
	if err != nil {
		return nil, err
	}

	expected, err := fetchChecksum(ctx, checksums, name)
	if err != nil {
		return nil, err
	}

	body, err := fetchBytes(ctx, artifact)
	if err != nil {
		return nil, err
	}

	if err := VerifyChecksum(body, expected, name); err != nil {
		return nil, err
	}
	return body, nil
}

// VerifyChecksum compares content against an expected SHA-256.
//
// Named and separate so the refusal can be tested without a network round
// trip: a test that reimplements the comparison proves the test, not the code.
func VerifyChecksum(content []byte, expected, name string) error {
	sum := sha256.Sum256(content)
	got := hex.EncodeToString(sum[:])
	if got == strings.ToLower(expected) {
		return nil
	}
	return fmt.Errorf(
		"checksum mismatch for %s (expected %s, got %s) — "+
			"the download does not match what the release says it should be, "+
			"so nothing has been changed", name, expected, got)
}

// fetchChecksum reads one entry out of a sha256sum-format checksums file.
func fetchChecksum(ctx context.Context, url, name string) (string, error) {
	body, err := fetchBytes(ctx, url)
	if err != nil {
		return "", fmt.Errorf("fetching checksums: %w", err)
	}
	return checksumFor(string(body), name)
}

// checksumFor reads one entry out of a sha256sum-format file.
func checksumFor(body, name string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// sha256sum writes "<hex>  <name>" and marks binary mode with a "*".
		if strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("the release's checksums.txt has no entry for %s", name)
}

// fetchBytes downloads a URL with a bounded read.
func fetchBytes(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mddb-cli/"+CurrentVersion())

	resp, err := http.DefaultClient.Do(req) // #nosec G107 -- built from a pinned base, see artifactURL
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxArtifactBytes {
		return nil, fmt.Errorf("%s is larger than the %d-byte limit", url, maxArtifactBytes)
	}
	return body, nil
}

// ExtractBinary pulls one named file out of a gzipped tarball.
//
// Only the basename is matched and no path from the archive is ever used, so a
// tarball containing "../../etc/cron.d/x" writes nothing anywhere — the classic
// path traversal in archive extraction cannot apply to a function that does not
// extract to paths.
func ExtractBinary(tarball []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(tarball)))
	if err != nil {
		return nil, fmt.Errorf("reading archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(header.Name) != name {
			continue
		}
		content, err := io.ReadAll(io.LimitReader(reader, maxArtifactBytes))
		if err != nil {
			return nil, err
		}
		return content, nil
	}
	return nil, fmt.Errorf("the archive does not contain %s", name)
}

// ReplaceBinary swaps the running binary for new content, atomically.
//
// The new file is written beside the old one, because rename is only atomic
// within a filesystem and /tmp is frequently a different one. The old binary is
// kept as .bak: an update that turns out to be wrong should be undoable by
// someone who cannot download anything.
func ReplaceBinary(path string, content []byte) (backup string, err error) {
	dir := filepath.Dir(path)

	temp, err := os.CreateTemp(dir, ".mddb-update-*")
	if err != nil {
		return "", fmt.Errorf("cannot write to %s: %w — updating a binary here "+
			"needs permission to write to the directory, so try again with sudo "+
			"or move the binary somewhere you own", dir, err)
	}
	tempPath := temp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err = temp.Write(content); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err = temp.Close(); err != nil {
		return "", err
	}
	if err = os.Chmod(tempPath, 0o755); err != nil { // #nosec G302 -- an executable has to be executable
		return "", err
	}

	backup = path + ".bak"
	// Rename rather than copy: the running binary's inode stays intact, so the
	// process executing this code is unaffected by its own replacement.
	if err = os.Rename(path, backup); err != nil {
		return "", fmt.Errorf("cannot move the current binary aside: %w", err)
	}
	if err = os.Rename(tempPath, path); err != nil {
		// Put back what was there. Failing halfway must not leave the machine
		// without a working binary.
		_ = os.Rename(backup, path)
		return "", fmt.Errorf("cannot install the new binary: %w", err)
	}
	return backup, nil
}
