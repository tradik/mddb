package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// The glob cases are shared with mddb-cli (its autoindex matchAny), so a
// git sync and autoindex select the same files (#421).
const globCasesPath = "../../testdata/globs/cases.json"

func TestMatchSyncGlobsSharedCases(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean(globCasesPath))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string   `json:"name"`
		Globs []string `json:"globs"`
		Path  string   `json:"path"`
		Match bool     `json:"match"`
	}
	if err := json.Unmarshal(data, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("the shared cases: %v", err)
	}
	for _, c := range cases {
		if got := matchSyncGlobs(c.Globs, c.Path); got != c.Match {
			t.Errorf("%s: matchSyncGlobs(%v, %q) = %v", c.Name, c.Globs, c.Path, got)
		}
	}
}

// git hash-object prints these for an empty file and "hello\n".
func TestGitBlobID(t *testing.T) {
	if got := gitBlobID(nil); got != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
		t.Errorf("empty: %s", got)
	}
	if got := gitBlobID([]byte("hello\n")); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Errorf("hello: %s", got)
	}
}

func TestCheckGitSync(t *testing.T) {
	base := func(mut func(*SyncAction)) *SyncAction {
		a := &SyncAction{Source: SyncSourceGit, URL: " https://github.com/tradik/mddb.git/ ", Collection: "c", Lang: "en"}
		mut(a)
		return a
	}
	bad := map[string]*SyncAction{
		"over https":         base(func(a *SyncAction) { a.URL = "http://github.com/a/b" }),
		"without credential": base(func(a *SyncAction) { a.URL = "https://u:p@github.com/a/b" }),
		"or a query":         base(func(a *SyncAction) { a.URL = "https://github.com/a/b?x=1" }),
		"(owner/repo)":       base(func(a *SyncAction) { a.URL = "https://github.com/a" }),
		"self-hosted":        base(func(a *SyncAction) { a.URL = "https://git.example.com/a/b" }),
		"forge (github":      base(func(a *SyncAction) { a.Forge = "bitbucket" }),
		"is not a branch":    base(func(a *SyncAction) { a.Ref = "a..b" }),
		"sync glob":          base(func(a *SyncAction) { a.Include = []string{"[x"} }),
		"sync secret name":   base(func(a *SyncAction) { a.HookSecretRef = "a b" }),
	}
	for want, a := range bad {
		if err := a.normalize(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	for _, ref := range []string{"-x", "/x", "x/", "a b", "a\x01"} {
		if checkGitRef(ref) == nil {
			t.Errorf("ref %q accepted", ref)
		}
	}
	a := base(func(a *SyncAction) {
		a.Ref, a.Forge, a.TokenSecretRef = " latest-release ", " ", "tok"
		a.URL = "https://git.example.com/group/sub/repo"
		a.Forge = "GitLab"
	})
	if err := a.normalize(); err != nil || a.Ref != gitRefLatestRelease || a.Forge != forgeGitLab {
		t.Fatalf("a self-hosted GitLab: %v %+v", err, a)
	}
	if a.describe() != "https://git.example.com/group/sub/repo@latest-release" {
		t.Errorf("describe: %s", a.describe())
	}
	ok := base(func(*SyncAction) {})
	if err := ok.normalize(); err != nil || ok.URL != "https://github.com/tradik/mddb.git" || ok.describe() != ok.URL+"@HEAD" {
		t.Errorf("github.com: %v %q", err, ok.URL)
	}
	if ok.fingerprint() == a.fingerprint() || len(ok.fingerprint()) != 16 {
		t.Error("fingerprints")
	}
}

// Each forge's URLs and credentials.
func TestGitForgeURLs(t *testing.T) {
	gh, _ := parseGitRepo("https://github.com/tradik/mddb.git", "")
	ghes, _ := parseGitRepo("https://ghe.example.com/a/b", forgeGitHub)
	gl, _ := parseGitRepo("https://gitlab.com/g/sub/r", "")
	cb, _ := parseGitRepo("https://codeberg.org/o/r", "")
	cases := map[string]string{
		gh.infoRefsURL():              "https://github.com/tradik/mddb.git/info/refs?service=git-upload-pack",
		gh.latestReleaseURL():         "https://api.github.com/repos/tradik/mddb/releases/latest",
		gh.archiveURL("c1", false):    "https://codeload.github.com/tradik/mddb/tar.gz/c1",
		gh.archiveURL("c1", true):     "https://api.github.com/repos/tradik/mddb/tarball/c1",
		gh.commitURL("c1"):            "https://github.com/tradik/mddb/commit/c1",
		ghes.latestReleaseURL():       "https://ghe.example.com/api/v3/repos/a/b/releases/latest",
		ghes.archiveURL("c1", false):  "https://ghe.example.com/api/v3/repos/a/b/tarball/c1",
		gl.latestReleaseURL():         "https://gitlab.com/api/v4/projects/g%2Fsub%2Fr/releases?order_by=released_at&sort=desc&per_page=1",
		gl.archiveURL("c1", false):    "https://gitlab.com/api/v4/projects/g%2Fsub%2Fr/repository/archive.tar.gz?sha=c1",
		gl.commitURL("c1"):            "https://gitlab.com/g/sub/r/-/commit/c1",
		cb.latestReleaseURL():         "https://codeberg.org/api/v1/repos/o/r/releases/latest",
		cb.archiveURL("c1", false):    "https://codeberg.org/api/v1/repos/o/r/archive/c1.tar.gz",
		cb.commitURL("c1"):            "https://codeberg.org/o/r/commit/c1",
		cb.header("", false).Get("A"): "",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
	if h := gh.header("tok", true).Get("Authorization"); h != "Basic eC1hY2Nlc3MtdG9rZW46dG9r" {
		t.Errorf("github smart HTTP: %s", h)
	}
	if h := gl.header("tok", false).Get("Authorization"); h != "Bearer tok" {
		t.Errorf("gitlab API: %s", h)
	}
	if h := cb.header("", false); h.Get("Authorization") != "" || h.Get("User-Agent") != syncUserAgent {
		t.Errorf("no token: %v", h)
	}
	s3 := &SyncAction{Source: SyncSourceS3}
	broken := &SyncAction{Source: SyncSourceGit, URL: "https://unknown.example/a/b"}
	if s3.commitURL("c") != "" || broken.commitURL("c") != "" || (*SyncAction)(nil).commitURL("c") != "" {
		t.Error("commitURL of no git source")
	}
}

// Each forge's archive of a run.
func TestGitSyncEachForgeArchive(t *testing.T) {
	for _, forge := range []string{forgeGitLab, forgeGitHub} {
		t.Run(forge, func(t *testing.T) {
			s := newBackupTestServer(t)
			f := newFakeForge(t, forge)
			f.commit("refs/heads/main", commitA, map[string]string{"a.md": "# A"})
			if out, err := runSync(t, s, syncRule(t, s, gitSyncAction(f, ""))); err != nil || out.counts.Added != 1 || out.counts.Ref != "main" {
				t.Errorf("%+v, %v", out, err)
			}
		})
	}
}

func TestParseGitRefs(t *testing.T) {
	bad := map[string]string{
		"":                       "no refs",
		"zzzz":                   "not a pkt-line",
		"0010abc":                "truncated",
		"00":                     "truncated",
		pktLine("# service\n"):   "no refs",
		pktLine("nothex HEAD\n"): "no refs",
	}
	for body, want := range bad {
		if _, err := parseGitRefs([]byte(body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", body, err)
		}
	}
	// No symref capability and no HEAD line: "" is HEAD, which is unknown.
	refs, err := parseGitRefs([]byte(pktLine(commitA + " refs/heads/x\n")))
	if err != nil || refs.head != "" {
		t.Fatal(err)
	}
	if _, _, err := refs.resolve(""); err == nil {
		t.Error("an unknown HEAD resolved")
	}
	if refs.highestSemverTag() != "" {
		t.Error("a semver tag out of nothing")
	}
}

func TestReadPktLineReaderError(t *testing.T) {
	if _, err := readPktLine(bufio.NewReader(iotestErr{})); err == nil || errors.Is(err, io.EOF) {
		t.Errorf("a failing reader: %v", err)
	}
}

// iotestErr fails every read.
type iotestErr struct{}

func (iotestErr) Read([]byte) (int, error) { return 0, errors.New("broken") }

// latestReleaseTag on answers that are not a release.
func TestLatestReleaseTagAnswers(t *testing.T) {
	f := newFakeForge(t, forgeGitLab)
	repo, _ := parseGitRepo(f.repoURL(), forgeGitLab)
	f.release = ""
	if tag, err := latestReleaseTag(t.Context(), repo, ""); tag != "" || err != nil {
		t.Errorf("no release: %q %v", tag, err)
	}
	f.forge = forgeGitea // the GitLab path now answers with an object, which is no list
	f.release = "v1"
	gitea, _ := parseGitRepo(f.repoURL(), forgeGitea)
	if tag, err := latestReleaseTag(t.Context(), gitea, ""); tag != "v1" || err != nil {
		t.Errorf("gitea: %q %v", tag, err)
	}
	f.token = "x"
	if _, err := latestReleaseTag(t.Context(), gitea, ""); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("refused: %v", err)
	}
	if _, err := readGitRefs(t.Context(), gitea, "wrong"); err == nil {
		t.Error("refs with a wrong token")
	}
}

func TestReadGitArchiveEdges(t *testing.T) {
	lim := gitArchiveLimits{maxFiles: 10, maxBytes: 100}
	// A file at the top level (no forge directory) and a Windows path.
	files, _, err := readGitArchive(bytes.NewReader(tarGz(tarEntry{name: "top.md", body: "x"})), lim)
	if err != nil || len(files) != 0 {
		t.Errorf("a top-level file: %v %v", files, err)
	}
	for _, name := range []string{"/abs/x.md", `C:\x.md`, `\x.md`} {
		if _, _, err := readGitArchive(bytes.NewReader(tarGz(tarEntry{name: name, body: "x"})), lim); !errors.Is(err, errGitArchiveUnsafe) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A truncated archive fails while reading a file.
	full := tarGz(tarEntry{name: "r/a.md", body: strings.Repeat("y", 90)})
	if _, _, err := readGitArchive(bytes.NewReader(full[:len(full)-30]), lim); err == nil {
		t.Error("a truncated archive")
	}
	c := &countingReader{r: strings.NewReader("abcdef"), limit: 3}
	if _, err := io.ReadAll(c); err == nil || !strings.Contains(err.Error(), "larger than 3") {
		t.Errorf("the uncompressed cap: %v", err)
	}
	if _, keep, err := readGitEntry(failingTar{}, &tar.Header{Size: 5}, "x.md", 100); keep || err == nil {
		t.Errorf("an unreadable entry: %v", err)
	}
}

// failingTar fails every read, as a broken archive stream.
type failingTar struct{}

func (failingTar) Read([]byte) (int, error) { return 0, errors.New("broken") }

func TestGitConvertFormat(t *testing.T) {
	for rel, want := range map[string]string{"a.PDF": "pdf", "b.html": "html", "c.md": "", "d.go": "", "e.docx": "docx"} {
		if got := gitConvertFormat(rel); got != want {
			t.Errorf("%s: %q", rel, got)
		}
	}
	if storedFormat("") != "txt" || storedFormat("pdf") != "pdf" {
		t.Error("storedFormat")
	}
	g := &gitSyncSource{ref: "v1", commit: commitA}
	if m := g.fileMeta(gitFile{path: "notes.txt"}, ""); m["kind"][0] != "text" {
		t.Errorf("a text file: %v", m)
	}
}
