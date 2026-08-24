package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// Self-update: finding out whether a newer release exists (OPS-019).
//
// MDDB ships as a single binary, and someone who downloaded one onto a VPS had
// no way to learn that a newer one exists — so they stayed on the version they
// first installed, security fixes included. That is the gap this closes.
//
// Two deliberate limits, both stated rather than hidden:
//
//   - **The daemon never replaces itself.** mddbd is a data server; an
//     unexpected restart is an incident, not a convenience. It can report that
//     an update exists and stops there.
//   - **Artifacts are verified by SHA-256 from the release's `checksums.txt`,
//     not by signature.** A checksum served from the same release as the
//     binary proves the download was not corrupted or swapped in transit. It
//     does not prove the release itself is genuine: whoever can publish a
//     release can publish a matching checksum. Signing would close that and is
//     not configured for this project yet — `docs/INSTALLATION.md` says so in
//     the same words rather than leaving it to be inferred.

// The endpoints are pinned: nothing a user types reaches them, which is the
// property that matters — an update path that can be aimed is a way to install
// someone else's binary.
//
// They are package variables rather than constants only so tests can point
// them at an httptest server. Nothing outside this package can reach them,
// there is no flag or environment variable that sets them, and the values here
// are the ones every build uses.
var (
	releaseAPIURL       = "https://api.github.com/repos/tradik/mddb/releases/latest"
	releaseDownloadBase = "https://github.com/tradik/mddb/releases/download"
)

const updateCheckTimeout = 10 * time.Second

// ReleaseInfo is what an update check found.
type ReleaseInfo struct {
	Version     string
	URL         string
	PublishedAt string
}

// githubRelease is the slice of GitHub's release JSON that is used.
type githubRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
}

// dockerMarkerPath is the file Docker places in every container. A variable
// only so a test can point it somewhere it can create; nothing outside this
// package can reach it.
var dockerMarkerPath = "/.dockerenv"

// InstallMethod is how this binary got onto the machine.
type InstallMethod string

const (
	InstallBinary InstallMethod = "binary"
	InstallSnap   InstallMethod = "snap"
	InstallDocker InstallMethod = "docker"
)

// DetectInstallMethod reports how this binary is packaged.
//
// Self-update refuses on anything but a plain binary: replacing a file a
// package manager owns leaves the manager's record disagreeing with the disk,
// and the next `snap refresh` silently undoes the update anyway.
func DetectInstallMethod() InstallMethod {
	// Snap sets SNAP for every confined process, and the binary lives on a
	// read-only squashfs, so a write would fail even if we tried.
	if os.Getenv("SNAP") != "" || os.Getenv("SNAP_NAME") != "" {
		return InstallSnap
	}
	if _, err := os.Stat(dockerMarkerPath); err == nil {
		return InstallDocker
	}
	return InstallBinary
}

// UpdateInstructions returns how to update through the channel that owns this
// binary, for the methods self-update refuses to touch.
func UpdateInstructions(method InstallMethod) string {
	switch method {
	case InstallSnap:
		return "installed as a snap — update with: sudo snap refresh mddb"
	case InstallDocker:
		return "running inside a container — update with: docker pull tradik/mddb:latest"
	default:
		return ""
	}
}

// FetchLatestRelease asks GitHub for the newest published release.
//
// Drafts and pre-releases are skipped: `releases/latest` already excludes them,
// and the check is belt and braces for a hand-published release.
func FetchLatestRelease(ctx context.Context) (*ReleaseInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "mddb-cli/"+CurrentVersion())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		// GitHub rate-limits unauthenticated requests per IP. Worth naming,
		// because "403" on its own reads like a permissions problem.
		return nil, fmt.Errorf("GitHub rate-limited the update check (status %d); try again later", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("reading release information: %w", err)
	}
	if release.Draft || release.Prerelease {
		return nil, fmt.Errorf("the latest release is a draft or pre-release")
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("release information carried no version")
	}

	return &ReleaseInfo{
		Version:     release.TagName,
		URL:         release.HTMLURL,
		PublishedAt: release.PublishedAt,
	}, nil
}

// platformSuffix is the GOOS-GOARCH pair release artifacts are named with.
func platformSuffix() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// artifactName is the tarball for this binary, version and platform.
func artifactName(binary, version string) string {
	return fmt.Sprintf("%s-%s-%s.tar.gz", binary, version, platformSuffix())
}

// artifactURL builds the download URL from the pinned base.
//
// The version reaches this from GitHub's own API rather than from a user, and
// it is still constrained: anything but a tag-shaped string is refused, so a
// crafted release name cannot walk out of the release path.
func artifactURL(version, name string) (string, error) {
	if !isTagShaped(version) {
		return "", fmt.Errorf("release version %q is not a version tag", version)
	}
	return fmt.Sprintf("%s/%s/%s", releaseDownloadBase, version, name), nil
}

// isTagShaped reports whether s is safe to place in a URL path segment.
func isTagShaped(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r == '.' || r == '-' || r == '_' || r == '+':
		default:
			return false
		}
	}
	return !strings.Contains(s, "..")
}
