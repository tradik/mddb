package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeForge is a forge for the git sync tests (#421): one repository,
// acme/docs, whose refs it advertises over git's smart HTTP protocol, whose
// latest release its API names, and whose commits it serves as tar.gz
// archives — on the paths of the forge it plays (github for a GitHub
// Enterprise Server, gitlab, gitea).
type fakeForge struct {
	*httptest.Server
	forge string
	mu    sync.Mutex
	refs  map[string]string // full name → object id (an annotated tag's tag object)
	// peeled maps an annotated tag to its commit.
	peeled  map[string]string
	head    string
	commits map[string]map[string]string // commit → path → content
	// raw serves a commit as these bytes instead of a built archive.
	raw map[string][]byte
	// release is the latest release's tag; "" answers 404 (no release).
	release string
	// releaseBody and refsStatus replace the answers when set.
	releaseBody string
	refsStatus  int
	// token, when set, is the credential every request must carry.
	token    string
	auth     []string // the Authorization headers seen
	archives int      // archive downloads
}

// The fixture's commits.
var (
	commitA = strings.Repeat("a", 40)
	commitB = strings.Repeat("b", 40)
	commitC = strings.Repeat("c", 40)
	tagObj  = strings.Repeat("d", 40)
)

func newFakeForge(t *testing.T, forge string) *fakeForge {
	t.Helper()
	f := &fakeForge{forge: forge, refs: map[string]string{}, peeled: map[string]string{},
		head: "refs/heads/main", commits: map[string]map[string]string{}, raw: map[string][]byte{}}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	prev := gitSyncClient
	gitSyncClient = func() *http.Client {
		c := f.Client()
		c.CheckRedirect = httpsOnlyRedirect(func(*http.Request, []*http.Request) error { return nil })
		return c
	}
	t.Cleanup(func() { gitSyncClient = prev })
	return f
}

// repoURL is the repository's URL on the fake forge.
func (f *fakeForge) repoURL() string { return f.URL + "/acme/docs.git" }

// commit sets a commit's files and points a ref at it.
func (f *fakeForge) commit(ref, id string, files map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits[id] = files
	f.refs[ref] = id
}

// archiveCount is how many archives were downloaded.
func (f *fakeForge) archiveCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.archives }

// authSeen is the Authorization headers seen.
func (f *fakeForge) authSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}

func (f *fakeForge) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	if !f.authorized(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	p := r.URL.EscapedPath()
	switch {
	case p == "/acme/docs.git/info/refs" && f.refsStatus != 0:
		w.WriteHeader(f.refsStatus)
	case p == "/acme/docs.git/info/refs" && r.URL.Query().Get("service") == "git-upload-pack":
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		_, _ = w.Write([]byte(f.advertisement()))
	case p == f.releasePath():
		f.serveRelease(w)
	default:
		f.serveArchive(w, r, p)
	}
}

// authorized checks a request's credential against the token: basic auth
// with it as the password, or "<scheme> <token>".
func (f *fakeForge) authorized(h string) bool {
	if f.token == "" {
		return true
	}
	if b, ok := strings.CutPrefix(h, "Basic "); ok {
		raw, _ := base64.StdEncoding.DecodeString(b)
		return strings.HasSuffix(string(raw), ":"+f.token)
	}
	return strings.HasSuffix(h, " "+f.token)
}

// releasePath is the forge's latest-release call.
func (f *fakeForge) releasePath() string {
	switch f.forge {
	case forgeGitLab:
		return "/api/v4/projects/acme%2Fdocs/releases"
	case forgeGitHub:
		return "/api/v3/repos/acme/docs/releases/latest"
	}
	return "/api/v1/repos/acme/docs/releases/latest"
}

func (f *fakeForge) serveRelease(w http.ResponseWriter) {
	switch {
	case f.releaseBody != "":
		_, _ = w.Write([]byte(f.releaseBody))
	case f.release == "":
		http.NotFound(w, nil)
	case f.forge == forgeGitLab:
		_, _ = fmt.Fprintf(w, `[{"tag_name":%q},{"tag_name":"v0.0.1"}]`, f.release)
	default:
		_, _ = fmt.Fprintf(w, `{"tag_name":%q,"name":"release"}`, f.release)
	}
}

// serveArchive answers the forge's archive path of a commit.
func (f *fakeForge) serveArchive(w http.ResponseWriter, r *http.Request, p string) {
	var commit string
	switch f.forge {
	case forgeGitLab:
		if p == "/api/v4/projects/acme%2Fdocs/repository/archive.tar.gz" {
			commit = r.URL.Query().Get("sha")
		}
	case forgeGitHub:
		commit, _ = strings.CutPrefix(p, "/api/v3/repos/acme/docs/tarball/")
	default:
		if s, ok := strings.CutPrefix(p, "/api/v1/repos/acme/docs/archive/"); ok {
			commit = strings.TrimSuffix(s, ".tar.gz")
		}
	}
	files, known := f.commits[commit]
	if raw, ok := f.raw[commit]; ok {
		f.archives++
		_, _ = w.Write(raw)
		return
	}
	if !known {
		http.NotFound(w, r)
		return
	}
	f.archives++
	entries := make([]tarEntry, 0, len(files))
	for name, body := range files {
		entries = append(entries, tarEntry{name: "docs-" + commit + "/" + name, body: body})
	}
	_, _ = w.Write(tarGz(entries...))
}

// advertisement is the smart HTTP ref advertisement of the refs.
func (f *fakeForge) advertisement() string {
	var b strings.Builder
	b.WriteString(pktLine("# service=git-upload-pack\n"))
	b.WriteString("0000")
	names := make([]string, 0, len(f.refs))
	for n := range f.refs {
		names = append(names, n)
	}
	sort.Strings(names)
	head := f.refs[f.head]
	b.WriteString(pktLine(head + " HEAD\x00multi_ack side-band-64k symref=HEAD:" + f.head + " agent=git/2.47\n"))
	for _, n := range names {
		b.WriteString(pktLine(f.refs[n] + " " + n + "\n"))
		if c := f.peeled[n]; c != "" {
			b.WriteString(pktLine(c + " " + n + gitPeeledSuffix + "\n"))
		}
	}
	b.WriteString("0000")
	return b.String()
}

// pktLine frames a payload as a pkt-line.
func pktLine(s string) string { return fmt.Sprintf("%04x%s", len(s)+4, s) }

// tarEntry is one entry of a test archive.
type tarEntry struct {
	name     string
	body     string
	typeflag byte // tar.TypeReg when 0
	linkname string
}

// tarGz builds a tar.gz with GitHub's pax global header first.
func tarGz(entries ...tarEntry) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header",
		PAXRecords: map[string]string{"comment": commitA}})
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	modified := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0o644, ModTime: modified, Linkname: e.linkname}
		if hdr.Typeflag == 0 {
			hdr.Typeflag = tar.TypeReg
		}
		if hdr.Typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		_ = tw.WriteHeader(hdr)
		if hdr.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// gitSyncAction is a git sync of the fake forge's repository.
func gitSyncAction(f *fakeForge, ref string) *SyncAction {
	return &SyncAction{Source: SyncSourceGit, URL: f.repoURL(), Collection: "repo", Lang: "en", Prune: true,
		GitSyncFields: GitSyncFields{Ref: ref, Forge: f.forge}}
}
