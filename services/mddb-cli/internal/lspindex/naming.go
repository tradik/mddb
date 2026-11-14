package lspindex

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// How the server is named in symIndex.

// serverName is Result.Server: the table's name for the server (or the one
// it reports), a slash, and its version, both made safe for symIndex.
func serverName(cand Candidate, info serverInfo) string {
	name := cand.Name
	if name == "" {
		name = info.Name
	}
	if name == "" {
		name = filepath.Base(cand.Argv[0])
	}
	return safeToken(name) + "/" + safeToken(serverVersion(info.Version))
}

// serverVersion extracts a version. gopls reports its whole build info as
// JSON in serverInfo.version; its top-level Version is the release.
func serverVersion(v string) string {
	if strings.HasPrefix(v, "{") {
		var bi struct{ Version string }
		_ = json.Unmarshal([]byte(v), &bi)
		v = bi.Version
	}
	// phpactor reports "phpactor/2026.10.07.0"; the name is already in front
	// of the slash serverName adds, so keep only what follows the last one.
	if i := strings.LastIndexByte(v, '/'); i >= 0 {
		v = v[i+1:]
	}
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return "unknown"
	}
	return v
}

// safeToken replaces characters symIndex cannot carry.
func safeToken(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '|' || r == '#' || hasSpace(string(r)) {
			return '-'
		}
		return r
	}, s)
}
