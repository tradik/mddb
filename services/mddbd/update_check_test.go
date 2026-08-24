package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// OPS-019: the daemon never replaces itself — it is a data server, and an
// unexpected restart is an incident. It reports and stops there.

func withUpdateServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	original := updateReleaseURL
	updateReleaseURL = server.URL
	t.Cleanup(func() { updateReleaseURL = original })
}

func TestCheckForUpdateFindsANewerRelease(t *testing.T) {
	withUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0"}`))
	})

	status := CheckForUpdate(context.Background())
	if !status.Available {
		t.Errorf("a much newer release was not reported: %+v", status)
	}
	if status.Latest != "v99.0.0" || status.Current != VERSION {
		t.Errorf("got %+v", status)
	}
	if status.Error != "" {
		t.Errorf("unexpected error: %s", status.Error)
	}
}

func TestCheckForUpdateOnAnOlderRelease(t *testing.T) {
	withUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.0.1"}`))
	})

	if status := CheckForUpdate(context.Background()); status.Available {
		t.Errorf("an older release was reported as an update: %+v", status)
	}
}

func TestCheckForUpdateIgnoresDraftsAndPreReleases(t *testing.T) {
	for name, body := range map[string]string{
		"draft":       `{"tag_name":"v99.0.0","draft":true}`,
		"pre-release": `{"tag_name":"v99.0.0","prerelease":true}`,
		"no tag":      `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			withUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			})
			status := CheckForUpdate(context.Background())
			if status.Available || status.Latest != "" {
				t.Errorf("a %s was treated as a release: %+v", name, status)
			}
		})
	}
}

// A failed check must be reported as a failure, not as "no update": a fleet
// that silently stops checking looks exactly like a fleet that is up to date.
func TestCheckForUpdateReportsFailuresRatherThanSilence(t *testing.T) {
	t.Run("error status", func(t *testing.T) {
		withUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		if status := CheckForUpdate(context.Background()); status.Error == "" {
			t.Error("a 500 produced no error")
		}
	})

	t.Run("garbage", func(t *testing.T) {
		withUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>"))
		})
		if status := CheckForUpdate(context.Background()); status.Error == "" {
			t.Error("HTML produced no error")
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		original := updateReleaseURL
		updateReleaseURL = "http://127.0.0.1:1/"
		defer func() { updateReleaseURL = original }()

		if status := CheckForUpdate(context.Background()); status.Error == "" {
			t.Error("an unreachable host produced no error")
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		original := updateReleaseURL
		updateReleaseURL = "://nonsense"
		defer func() { updateReleaseURL = original }()

		if status := CheckForUpdate(context.Background()); status.Error == "" {
			t.Error("an invalid URL produced no error")
		}
	})
}

func TestUpdateCheckEnabled(t *testing.T) {
	t.Setenv("MDDB_UPDATE_CHECK", "")
	if !UpdateCheckEnabled() {
		t.Error("the check should be on by default")
	}
	t.Setenv("MDDB_UPDATE_CHECK", "0")
	if UpdateCheckEnabled() {
		t.Error("MDDB_UPDATE_CHECK=0 did not turn it off")
	}
	// Anything other than "0" leaves it on; a typo must not silently disable
	// the thing that surfaces security releases.
	t.Setenv("MDDB_UPDATE_CHECK", "false")
	if !UpdateCheckEnabled() {
		t.Error(`only "0" should disable the check`)
	}
}

func TestCompareReleaseVersions(t *testing.T) {
	// The same rule as mddb-cli's CompareVersions; the two are written twice
	// because the modules are separate by design, so both need pinning.
	cases := []struct {
		a, b string
		want int
	}{
		{"v2.12.0", "2.11.4", 1},
		{"2.11.4", "v2.12.0", -1},
		{"v2.12.0", "2.12.0", 0},
		{"v2.10.0", "2.9.9", 1},
		{"v2.12.0", "2.12.0-rc1", 1},
		{"v2.12.0-rc1", "2.12.0", -1},
		{"v2.12.0-rc1", "2.12.0-rc2", -1},
		{"v2.12.0-rc2", "2.12.0-rc1", 1},
		{"v2.12.0-rc1", "2.12.0-rc1", 0},
	}
	for _, c := range cases {
		if got := compareReleaseVersions(c.a, c.b); got != c.want {
			t.Errorf("compareReleaseVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareReleaseVersionsSurvivesNonsense(t *testing.T) {
	// An unparseable version must not be able to stop a server from starting.
	for _, c := range [][2]string{{"", "2.12.0"}, {"x.y.z", "2.12.0"}, {"v2", "2.0.0"}, {"", ""}} {
		_ = compareReleaseVersions(c[0], c[1])
	}
}

func TestStartUpdateCheckRespectsTheOptOut(t *testing.T) {
	t.Setenv("MDDB_UPDATE_CHECK", "0")
	withUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the check ran despite the opt-out")
	})

	s := &Server{}
	s.startUpdateCheck()
	if s.UpdateStatus != nil {
		t.Error("a status was cached with the check disabled")
	}
}

// /health carries the cached answer, so a monitoring system can alert on a
// fleet running an old version rather than someone having to notice.
func TestHealthCarriesTheUpdateStatus(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	s.Ready = true
	s.UpdateStatus = &UpdateStatus{Current: "2.11.4", Latest: "v2.12.0", Available: true}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	body := w.Body.String()
	for _, want := range []string{`"update"`, `"available":true`, `"v2.12.0"`} {
		if !contains(body, want) {
			t.Errorf("health is missing %s:\n%s", want, body)
		}
	}
}

// An absent field says "we have not looked", which a monitoring system must be
// able to tell from "no update available".
func TestHealthOmitsTheUpdateStatusWhenUnchecked(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	s.Ready = true
	s.UpdateStatus = nil

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	if contains(w.Body.String(), `"update"`) {
		t.Errorf("an unchecked server reported an update status:\n%s", w.Body.String())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func TestDescribeUpdate(t *testing.T) {
	cases := []struct {
		name     string
		status   UpdateStatus
		wantCode int
		wantText string
	}{
		// The exit codes are the interesting part: a cron job or a CI step can
		// act on the difference without parsing prose.
		{"up to date", UpdateStatus{Current: "2.12.0"}, 0, "up to date"},
		{"available", UpdateStatus{Current: "2.11.4", Latest: "v2.12.0", Available: true}, 10, "is available"},
		{"failed", UpdateStatus{Current: "2.12.0", Error: "connection refused"}, 1, "check failed"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			message, code := describeUpdate(c.status)
			if code != c.wantCode {
				t.Errorf("exit code %d, want %d", code, c.wantCode)
			}
			if !contains(message, c.wantText) {
				t.Errorf("message %q does not contain %q", message, c.wantText)
			}
		})
	}
}

// An available update names every channel, because the daemon does not know
// which one installed it and guessing wrong sends the operator down a dead end.
func TestDescribeUpdateNamesEveryChannel(t *testing.T) {
	message, _ := describeUpdate(UpdateStatus{Current: "2.11.4", Latest: "v2.12.0", Available: true})
	for _, want := range []string{"snap refresh", "docker pull", "releases/latest", "does not replace itself"} {
		if !contains(message, want) {
			t.Errorf("message is missing %q:\n%s", want, message)
		}
	}
}

func TestStartUpdateCheckCachesWhatItFound(t *testing.T) {
	t.Setenv("MDDB_UPDATE_CHECK", "")
	withUpdateServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0"}`))
	})

	s := &Server{}
	<-s.startUpdateCheck()

	if s.UpdateStatus == nil {
		t.Fatal("nothing was cached")
	}
	if !s.UpdateStatus.Available || s.UpdateStatus.Latest != "v99.0.0" {
		t.Errorf("cached %+v", s.UpdateStatus)
	}
}

func TestStartUpdateCheckCachesAFailureToo(t *testing.T) {
	// A cached failure is what lets /health say "we have not been able to
	// look" instead of implying everything is fine.
	t.Setenv("MDDB_UPDATE_CHECK", "")
	original := updateReleaseURL
	updateReleaseURL = "http://127.0.0.1:1/"
	defer func() { updateReleaseURL = original }()

	s := &Server{}
	<-s.startUpdateCheck()

	if s.UpdateStatus == nil || s.UpdateStatus.Error == "" {
		t.Errorf("a failed check was not recorded: %+v", s.UpdateStatus)
	}
}

// The stdout/stderr split and the exit code, which describeUpdate alone cannot
// cover: they live above it, in the part that actually terminates the process.
func TestWriteUpdateReportRoutesOutputAndExitCode(t *testing.T) {
	cases := []struct {
		name       string
		status     UpdateStatus
		wantCode   int
		wantStdout bool
	}{
		{"up to date", UpdateStatus{Current: "2.12.0"}, 0, true},
		{"available", UpdateStatus{Current: "2.11.4", Latest: "v2.12.0", Available: true}, 10, true},
		// A failure goes to stderr so a shell can capture the answer without
		// capturing the diagnosis.
		{"failed", UpdateStatus{Current: "2.12.0", Error: "connection refused"}, 1, false},
	}

	original := updateExit
	defer func() { updateExit = original }()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			var got int
			updateExit = func(code int) { got = code }

			writeUpdateReport(&stdout, &stderr, c.status)

			if got != c.wantCode {
				t.Errorf("exit code %d, want %d", got, c.wantCode)
			}
			if c.wantStdout {
				if stdout.Len() == 0 || stderr.Len() != 0 {
					t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
				}
			} else {
				if stderr.Len() == 0 || stdout.Len() != 0 {
					t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
				}
			}
		})
	}
}
