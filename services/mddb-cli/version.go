package main

import (
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

// Version is the release this binary was built from.
//
// OPS-019: the release workflow already passes
// `-ldflags "-X main.Version=${VERSION}"`, and there was no `main.Version` for
// it to write to — Go's linker ignores `-X` for a symbol that does not exist,
// silently. So every released binary reported the `1.0.0` that was hardcoded
// into the command definition, no matter which release it came from. Nothing
// caught it because nothing compared the two.
//
// Self-update makes that a correctness problem rather than a cosmetic one: a
// binary that cannot say which version it is cannot tell whether a newer one
// exists.
var Version = ""

// devVersion is what a binary built outside the release pipeline reports.
const devVersion = "dev"

// CurrentVersion reports this binary's version.
//
// Three sources, in descending order of trust: the linker-injected value, the
// module version Go records for `go install`ed binaries, and finally "dev".
func CurrentVersion() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return devVersion
}

// IsDevelopmentBuild reports whether this binary came from outside a release.
//
// Self-update refuses on one: there is no release to compare against, and
// replacing a locally built binary with a published one would throw away
// whatever the developer was testing.
func IsDevelopmentBuild() bool {
	return CurrentVersion() == devVersion
}

// CompareVersions orders two semantic versions, returning -1, 0 or 1.
//
// A leading "v" is optional on either side, and pre-release suffixes are
// compared as strings after the numeric parts — enough to keep 2.12.0-rc1
// below 2.12.0, which is the only pre-release ordering this project produces.
// A component that is not a number sorts as zero rather than failing: an
// unparseable version must not be able to stop an update check.
func CompareVersions(a, b string) int {
	an, apre := splitVersion(a)
	bn, bpre := splitVersion(b)

	for i := 0; i < 3; i++ {
		if an[i] != bn[i] {
			if an[i] < bn[i] {
				return -1
			}
			return 1
		}
	}

	// Equal numerically. A release outranks its own pre-releases.
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

// splitVersion parses "v2.12.0-rc1" into [2 12 0] and "rc1".
func splitVersion(v string) ([3]int, string) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")

	var pre string
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}

	var out [3]int
	// SplitN caps the result at three, so every index is in range.
	for i, part := range strings.SplitN(v, ".", 3) {
		n, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		out[i] = n
	}
	return out, pre
}

// realpath resolves symlinks in a path.
//
// Separate so a test can drive the symlink case without creating one, and
// because `os.Executable` returning a symlink is common enough — a binary in
// /usr/local/bin linked from ~/bin — that replacing the link instead of the
// target would be a quiet no-op.
func realpath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
