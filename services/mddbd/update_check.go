package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	json "mddb/internal/jsonx"
)

// Update checking for the daemon (OPS-019).
//
// The daemon **never** replaces itself. mddbd is a data server; an unexpected
// restart is an incident, not a convenience. All it does is find out whether a
// newer release exists and say so — once at startup, and in `/health` so a
// monitoring system can alert on a fleet running an old version rather than
// someone having to notice.
//
// `mddb-cli self-update` is where the installing half lives, deliberately: the
// tool an operator runs on purpose, not the process holding their data.
//
// This is not shared code with mddb-cli. The two are separate Go modules and
// the server must not depend on the client module to make the reverse
// direction impossible; ~60 lines of duplication is the price, and it is
// cheaper than the dependency it avoids. The comparison rule is the same one
// as `mddb-cli/version.go` — change one and change the other.

// updateReleaseURL is pinned; it is a variable only so tests can point it at
// an httptest server. Nothing outside this package can reach it, and no flag
// or environment variable sets it.
var updateReleaseURL = "https://api.github.com/repos/tradik/mddb/releases/latest"

const updateTimeout = 10 * time.Second

// UpdateStatus is what an update check found.
type UpdateStatus struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	CheckedAt int64  `json:"checkedAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

// UpdateCheckEnabled reports whether the startup check should run.
//
// On by default. It is one GET to a pinned URL and carries nothing about this
// installation beyond the request itself — no identifier, no collection names,
// no counts. `MDDB_UPDATE_CHECK=0` turns it off for deployments where any
// outbound request at all needs a reason.
func UpdateCheckEnabled() bool {
	return os.Getenv("MDDB_UPDATE_CHECK") != "0"
}

// CheckForUpdate asks GitHub for the newest release and compares it to VERSION.
func CheckForUpdate(ctx context.Context) UpdateStatus {
	status := UpdateStatus{Current: VERSION, CheckedAt: time.Now().Unix()}

	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateReleaseURL, nil)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "mddbd/"+VERSION)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		status.Error = fmt.Sprintf("release API returned %d", resp.StatusCode)
		return status
	}

	var release struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		status.Error = err.Error()
		return status
	}
	if release.Draft || release.Prerelease || release.TagName == "" {
		return status
	}

	status.Latest = release.TagName
	status.Available = compareReleaseVersions(release.TagName, VERSION) > 0
	return status
}

// compareReleaseVersions orders two semantic versions, returning -1, 0 or 1.
//
// The same rule as mddb-cli's CompareVersions; see the note at the top of this
// file for why it is written twice. A component that is not a number sorts as
// zero: an unparseable version must not be able to stop a server from starting.
func compareReleaseVersions(a, b string) int {
	an, apre := splitReleaseVersion(a)
	bn, bpre := splitReleaseVersion(b)

	for i := 0; i < 3; i++ {
		if an[i] != bn[i] {
			if an[i] < bn[i] {
				return -1
			}
			return 1
		}
	}

	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	case apre < bpre:
		return -1
	default:
		return 1
	}
}

func splitReleaseVersion(v string) ([3]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")

	var pre string
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}

	var out [3]int
	// SplitN caps the result at three, so every index is in range.
	for i, part := range strings.SplitN(v, ".", 3) {
		if n, err := strconv.Atoi(part); err == nil {
			out[i] = n
		}
	}
	return out, pre
}

// reportUpdateAndExit answers `mddbd --check-update` and terminates.
//
// Exit codes are the interesting part: 0 when up to date, 10 when an update
// exists, 1 when the check failed. A cron job or a CI step can act on the
// difference without parsing prose, which is the whole reason the flag is
// preferable to reading the log.
func reportUpdateAndExit() {
	writeUpdateReport(os.Stdout, os.Stderr, CheckForUpdate(context.Background()))
}

// updateExit is os.Exit, replaced in tests so the routing above the call can
// be exercised. A function whose last statement is os.Exit is otherwise
// unreachable from a test, which leaves the part most likely to be wrong — the
// stdout/stderr split and the exit code — unchecked.
var updateExit = os.Exit

// writeUpdateReport prints a check result and terminates with its exit code.
//
// The failure goes to stderr and everything else to stdout, so a shell can
// capture the answer without capturing the diagnosis.
func writeUpdateReport(stdout, stderr io.Writer, status UpdateStatus) {
	message, code := describeUpdate(status)

	if code == 1 {
		_, _ = fmt.Fprint(stderr, message)
	} else {
		_, _ = fmt.Fprint(stdout, message)
	}
	updateExit(code)
}

// describeUpdate turns a check result into what to print and what to exit with.
//
// Split from reportUpdateAndExit so the decision can be tested; a function
// whose last statement is os.Exit cannot be called from a test at all.
func describeUpdate(status UpdateStatus) (message string, code int) {
	switch {
	case status.Error != "":
		return fmt.Sprintf("update check failed: %s\n", status.Error), 1
	case status.Available:
		return fmt.Sprintf(
			"mddbd %s — %s is available\n"+
				"The daemon does not replace itself. Update through the channel that installed it:\n"+
				"  binary:  download from https://github.com/tradik/mddb/releases/latest\n"+
				"  snap:    sudo snap refresh mddb\n"+
				"  docker:  docker pull tradik/mddb:latest\n",
			status.Current, status.Latest), 10
	default:
		return fmt.Sprintf("mddbd %s is up to date\n", status.Current), 0
	}
}

// startUpdateCheck runs the check in the background and caches the result.
//
// Background because a GitHub request must never be able to delay a data
// server's startup — a slow or unreachable network would otherwise add its
// timeout to every boot.
func (s *Server) startUpdateCheck() <-chan struct{} {
	done := make(chan struct{})
	if !UpdateCheckEnabled() {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		s.runUpdateCheck()
	}()
	// Returned so a test can wait for it. Nothing in the server does; the
	// point of the goroutine is that startup does not.
	return done
}

// runUpdateCheck performs the check and records what it found.
func (s *Server) runUpdateCheck() {
	status := CheckForUpdate(context.Background())
	s.UpdateStatus = &status

	switch {
	case status.Error != "":
		slog.Debug("update check failed", "err", status.Error)
	case status.Available:
		slog.Info("a newer release is available",
			"current", status.Current, "latest", status.Latest,
			"note", "mddbd does not update itself; set MDDB_UPDATE_CHECK=0 to stop checking")
	}
}
