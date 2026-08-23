package main

import "testing"

// OPS-019: the release workflow passes -X main.Version and there was no
// main.Version for it to write to, so every released binary reported the
// hardcoded 1.0.0. Self-update makes that a correctness problem: a binary that
// cannot say which version it is cannot tell whether a newer one exists.

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v2.12.0", "v2.11.4", 1},
		{"v2.11.4", "v2.12.0", -1},
		{"v2.12.0", "v2.12.0", 0},
		// The "v" is optional on either side; releases carry it, the mddbd
		// constant does not.
		{"2.12.0", "v2.12.0", 0},
		{"v2.12.0", "2.11.4", 1},
		// Component-wise, not lexicographic: "10" > "9".
		{"v2.10.0", "v2.9.9", 1},
		{"v3.0.0", "v2.99.99", 1},
		{"v2.12.1", "v2.12.0", 1},
		// A release outranks its own pre-releases.
		{"v2.12.0", "v2.12.0-rc1", 1},
		{"v2.12.0-rc1", "v2.12.0", -1},
		{"v2.12.0-rc2", "v2.12.0-rc1", 1},
		{"v2.12.0-rc1", "v2.12.0-rc2", -1},
		{"v2.12.0-rc1", "v2.12.0-rc1", 0},
		// Build metadata is treated like a pre-release suffix; it never makes
		// a version newer than the release it was built from.
		{"v2.12.0+build1", "v2.12.0", -1},
	}

	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// An unparseable version must not be able to stop an update check — it should
// compare as something, not panic or error out.
func TestCompareVersionsSurvivesNonsense(t *testing.T) {
	for _, c := range [][2]string{
		{"", "v2.12.0"},
		{"not-a-version", "v2.12.0"},
		{"v2.x.0", "v2.0.0"},
		{"v2", "v2.0.0"},
		{"v2.12.0.1", "v2.12.0"},
		{"", ""},
	} {
		_ = CompareVersions(c[0], c[1])
	}
}

func TestCurrentVersionFallsBackToDev(t *testing.T) {
	original := Version
	defer func() { Version = original }()

	Version = ""
	// In `go test` the build info main version is "" or "(devel)", so this
	// exercises the last fallback.
	if got := CurrentVersion(); got == "" {
		t.Error("CurrentVersion returned an empty string")
	}
}

func TestCurrentVersionPrefersTheInjectedValue(t *testing.T) {
	original := Version
	defer func() { Version = original }()

	Version = "v2.12.0"
	if got := CurrentVersion(); got != "v2.12.0" {
		t.Errorf("got %q, want the injected version", got)
	}
	if IsDevelopmentBuild() {
		t.Error("an injected version should not read as a development build")
	}
}

func TestIsDevelopmentBuild(t *testing.T) {
	original := Version
	defer func() { Version = original }()

	Version = devVersion
	if !IsDevelopmentBuild() {
		t.Error("the dev sentinel should read as a development build")
	}
}

func TestSplitVersion(t *testing.T) {
	nums, pre := splitVersion("v2.12.0-rc1")
	if nums != [3]int{2, 12, 0} || pre != "rc1" {
		t.Errorf("got %v %q", nums, pre)
	}
	nums, pre = splitVersion("  1.2.3  ")
	if nums != [3]int{1, 2, 3} || pre != "" {
		t.Errorf("whitespace was not trimmed: %v %q", nums, pre)
	}
}
