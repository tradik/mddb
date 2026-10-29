package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Reference repositories (AGT-003): someone else's code, cloned on this
// machine and indexed into a collection marked code.role "reference", so an
// agent can ask how they solved what it is about to solve. The server never
// clones anything and never takes a repository URL: fetching is the client's
// job, which keeps SSRF and path traversal off the server.

// referenceState is what a clone remembers about itself, in the clone.
type referenceState struct {
	URL        string `json:"url"`
	Ref        string `json:"ref,omitempty"`
	Collection string `json:"collection"`
	License    string `json:"license,omitempty"`
}

const referenceStateFile = ".mddb-reference.json"

// scpLike matches git's scp-style ssh address: user@host:path.
var scpLike = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^/].*$`)

// normalizeRepoURL accepts https://, ssh:// and user@host:path addresses, and
// a bare host/owner/repo as https. Anything else — file://, a local path, a
// git transport helper (ext::), a value starting with "-" that git would
// read as an option — is refused.
func normalizeRepoURL(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	switch {
	case repo == "" || strings.HasPrefix(repo, "-"):
		return "", errors.New("repository address is empty or starts with '-'")
	case strings.HasPrefix(repo, "https://"), strings.HasPrefix(repo, "ssh://"), scpLike.MatchString(repo):
		return repo, nil
	case strings.Contains(repo, "://") || strings.Contains(repo, "::"):
		return "", fmt.Errorf("unsupported repository address %q: use https:// or ssh://", repo)
	case strings.Count(repo, "/") >= 2 && strings.Contains(strings.SplitN(repo, "/", 2)[0], "."):
		return "https://" + repo, nil
	}
	return "", fmt.Errorf("unsupported repository address %q: use https://, ssh:// or host/owner/repo", repo)
}

// referenceName is the default collection for a repository: ref-<owner>-<repo>.
func referenceName(url string) string {
	trimmed := strings.TrimSuffix(strings.TrimRight(url, "/"), ".git")
	parts := strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == ':' })
	name := parts[len(parts)-1]
	if len(parts) >= 2 {
		name = parts[len(parts)-2] + "-" + name
	}
	return sanitizeCollection("ref-" + name)
}

// referencesRoot is where clones live: $MDDB_REFERENCES_DIR, else the user
// cache directory's mddb/references.
func referencesRoot() (string, error) {
	if dir := os.Getenv("MDDB_REFERENCES_DIR"); dir != "" {
		return dir, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "mddb", "references"), nil
}

// referenceDir is a reference's clone directory, which must stay inside root.
func referenceDir(name string) (string, error) {
	root, err := referencesRoot()
	if err != nil {
		return "", err
	}
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", fmt.Errorf("invalid reference name %q", name)
	}
	return filepath.Join(root, name), nil
}

// gitClone clones url at ref into dir, shallow. A variable so tests can clone
// from a local fixture, which the address check rightly refuses.
var gitClone = func(url, ref, dir string) error {
	args := []string{"clone", "--depth", "1", "--quiet"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	_, err := runGit(".", append(args, "--", url, dir)...)
	return err
}

// gitRefresh moves a shallow clone to the latest commit of its ref.
var gitRefresh = func(dir, ref string) error {
	if ref == "" {
		ref = "HEAD"
	}
	if _, err := runGit(dir, "fetch", "--depth", "1", "--quiet", "origin", ref); err != nil {
		return err
	}
	_, err := runGit(dir, "reset", "--hard", "--quiet", "FETCH_HEAD")
	return err
}

// licenseNames maps text found in a licence file to its SPDX identifier,
// most specific first.
var licenseNames = []struct{ needle, id string }{
	{"Apache License", "Apache-2.0"},
	{"GNU AFFERO GENERAL PUBLIC LICENSE", "AGPL-3.0"},
	{"GNU LESSER GENERAL PUBLIC LICENSE", "LGPL"},
	{"GNU GENERAL PUBLIC LICENSE", "GPL"},
	{"Mozilla Public License", "MPL-2.0"},
	{"Permission is hereby granted, free of charge", "MIT"},
	{"Redistribution and use in source and binary forms", "BSD"},
	{"Permission to use, copy, modify, and/or distribute", "ISC"},
	{"This is free and unencumbered software", "Unlicense"},
}

// detectLicense names a clone's licence from its LICENSE or COPYING file:
// an SPDX identifier when the text is recognised, "see <file>" when it is
// not, "unknown" when there is no such file. An agent copying code has to
// be able to see this.
func detectLicense(dir string) string {
	for _, name := range []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "LICENCE", "COPYING", "COPYING.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- a fixed name inside the clone
		if err != nil {
			continue
		}
		text := string(data)
		for _, l := range licenseNames {
			if strings.Contains(text, l.needle) {
				return l.id
			}
		}
		return "see " + name
	}
	return "unknown"
}

func writeReferenceState(dir string, st referenceState) error {
	data, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(filepath.Join(dir, referenceStateFile), data, 0o600)
}

func readReferenceState(dir string) (referenceState, error) {
	var st referenceState
	data, err := os.ReadFile(filepath.Join(dir, referenceStateFile)) // #nosec G304 -- inside the references root
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(data, &st)
}
