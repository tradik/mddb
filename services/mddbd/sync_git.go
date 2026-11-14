package main

import (
	"context"
	"crypto/sha1" // #nosec G505 -- git's object id is SHA-1 by definition; nothing is protected by it
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// A git repository as a synced source (#421). A run resolves the rule's ref
// to a commit (sync_git_refs.go); when that is the commit the last
// successful run read, with the rule unchanged since, the run ends there —
// no archive is downloaded. Otherwise it reads the commit's archive
// (sync_git_archive.go) and writes each included file whose git blob id
// differs from the syncHash its document carries. Keys and meta are the ones
// `mddb-cli autoindex` writes for the same file, so a collection seeded by
// autoindex can be taken over (adopt) without duplicates.

// errSyncUpToDate ends a run whose commit the last run already read.
var errSyncUpToDate = errors.New("the commit is the one the last run read")

// gitSyncSource reads a repository through its forge.
type gitSyncSource struct {
	action *SyncAction
	repo   gitRepo
	token  string
	// lastCommit is the commit the last successful run of the unchanged
	// rule read; "" when there is none.
	lastCommit string
	ref        string
	commit     string
	files      map[string]gitFile
}

// newGitSyncSource connects a rule's git source: its repository, its token
// (opened from the stored secret it names) and the commit its last
// successful run read.
func (s *Server) newGitSyncSource(rule AutomationRule) (*gitSyncSource, error) {
	a := rule.Sync
	repo, err := parseGitRepo(a.URL, a.Forge)
	if err != nil {
		return nil, err
	}
	g := &gitSyncSource{action: a, repo: repo, lastCommit: s.lastGitCommit(rule)}
	if a.TokenSecretRef != "" {
		if g.token, err = s.syncSecret(a.TokenSecretRef); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// syncSecret opens a stored secret a sync names: the chat's write-only
// store, sealed with the at-rest key (MCP-005).
func (s *Server) syncSecret(name string) (string, error) {
	if s.ChatScenarios == nil {
		return "", errChatScenariosUnavailable
	}
	_, value, err := s.ChatScenarios.McpSecretHeader(name)
	return value, err
}

// resolved is the ref and commit the run read, for its result.
func (g *gitSyncSource) resolved() (ref, commit string) { return g.ref, g.commit }

// list resolves the ref and, unless the commit is the last run's, reads its
// archive: one item per included file, keyed by its path.
func (g *gitSyncSource) list(ctx context.Context) ([]syncItem, bool, error) {
	ref, commit, err := resolveGitRef(ctx, g.repo, g.action.Ref, g.token)
	if err != nil {
		return nil, false, err
	}
	g.ref, g.commit = ref, commit
	if commit == g.lastCommit {
		return nil, false, errSyncUpToDate
	}
	files, truncated, err := g.readArchive(ctx)
	if err != nil {
		return nil, false, err
	}
	g.files = make(map[string]gitFile, len(files))
	items := make([]syncItem, 0, len(files))
	for _, f := range files {
		g.files[f.path] = f
		items = append(items, syncItem{ref: f.path, key: f.path, size: f.size})
	}
	return items, truncated, nil
}

// readArchive downloads the commit's archive and reads the included files.
func (g *gitSyncSource) readArchive(ctx context.Context) ([]gitFile, bool, error) {
	resp, err := openOutbound(ctx, gitSyncClient(), g.repo.archiveURL(g.commit, g.token != ""), g.repo.header(g.token, false))
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("downloading the archive of %s: %s", g.commit, resp.Status)
	}
	a := g.action
	return readGitArchive(resp.Body, gitArchiveLimits{maxFiles: a.MaxDocuments, maxBytes: a.MaxBytes,
		include: a.Include, exclude: a.Exclude})
}

// fetch hands over a file the archive held, unless its blob id is the one
// its document was written from.
func (g *gitSyncSource) fetch(_ context.Context, it syncItem, prev syncState) (syncFetched, error) {
	f := g.files[it.ref]
	if f.tooLarge {
		return syncFetched{}, fmt.Errorf("larger than maxBytes (%d)", g.action.MaxBytes)
	}
	id := gitBlobID(f.body)
	if id == prev.hash {
		return syncFetched{notModified: true}, nil
	}
	format := gitConvertFormat(f.path)
	return syncFetched{body: f.body, hash: id, format: storedFormat(format), meta: g.fileMeta(f, format),
		noFrontmatter: !docExtensions[strings.ToLower(path.Ext(f.path))]}, nil
}

// storedFormat is the converter format of a file: its own for a format
// the server converts, else text stored as it is.
func storedFormat(format string) string {
	if format == "" {
		return "txt"
	}
	return format
}

// gitBlobID is git's object id of a file's content: the SHA-1 of
// "blob <size>\0<content>" — what `git hash-object` prints.
func gitBlobID(content []byte) string {
	h := sha1.New() // #nosec G401 -- git's object id, not a protection // NOSONAR go:S4790 -- git defines blob ids as SHA-1
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(content))
	_, _ = h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// docExtensions are prose, stored as kind "doc" with their frontmatter
// read — as autoindex classifies them.
var docExtensions = map[string]bool{".md": true, ".markdown": true, ".mdx": true}

// formatHTML is the converter's name of HTML.
const formatHTML = "html"

// gitMarkupFormats are text formats the converter makes Markdown of, as
// autoindex sends them for conversion; the binary formats (pdf, docx, ...)
// are converted too.
var gitMarkupFormats = map[string]string{
	".html": formatHTML, ".htm": formatHTML, ".xhtml": formatHTML, ".tex": "tex", ".latex": "tex",
	".csv": "csv", ".tsv": "tsv", ".eml": "eml", ".mbox": "mbox", ".rtf": "rtf",
}

// gitConvertFormat is the format a file is converted from; "" for one
// stored as the text it is.
func gitConvertFormat(rel string) string {
	ext := strings.ToLower(path.Ext(rel))
	if f := gitMarkupFormats[ext]; f != "" {
		return f
	}
	if f := normalizeFormat(ext); binaryFormats[f] {
		return f
	}
	return ""
}

// fileMeta is the meta autoindex gives the same file — path, extension,
// size, modified, kind, language, gitCommit, gitBranch — and the ref and
// commit it was synced at.
func (g *gitSyncSource) fileMeta(f gitFile, format string) map[string][]string {
	ext := path.Ext(f.path)
	m := map[string][]string{
		"path": {f.path}, "extension": {ext}, "size": {strconv.FormatInt(f.size, 10)},
		"modified":  {f.modified.Format(time.RFC3339)},
		"gitCommit": {g.commit}, "gitBranch": {g.ref},
		syncMetaRef: {g.ref}, syncMetaCommit: {g.commit},
	}
	lower := strings.ToLower(ext)
	switch {
	case format != "", docExtensions[lower]:
		m[metaKind] = []string{"doc"}
	case codeExtensions[lower] != "":
		m[metaKind], m["language"] = []string{"code"}, []string{codeExtensions[lower]}
	default:
		m[metaKind] = []string{"text"}
	}
	return m
}

// lastGitCommit is the commit the newest successful run of rule read, when
// that run was of the rule as it is now (the same fingerprint): "" when
// there is none, so the run reads the archive.
func (s *Server) lastGitCommit(rule AutomationRule) string {
	if s.AutomationLogStore == nil {
		return ""
	}
	logs, _, err := s.AutomationLogStore.List(1, "", rule.ID, "success")
	if err != nil || len(logs) == 0 || logs[0].Sync == nil || logs[0].Sync.Fingerprint != rule.Sync.fingerprint() {
		return ""
	}
	return logs[0].Sync.Commit
}
