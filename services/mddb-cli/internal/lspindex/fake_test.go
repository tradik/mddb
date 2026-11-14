package lspindex

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// The fake language server (testdata/fakelsp) is built once per test binary
// into a temporary directory: CI installs no language server, and no binary
// is committed. It is built on first use, so the lspreal tests run without
// it — on purpose with a go on PATH too old to build anything.
var (
	fakeOnce sync.Once
	fakeDir  string
	fakeBin  string
	fakeErr  error
)

func TestMain(m *testing.M) {
	// Each run waits for the server to settle; the fake settles at once.
	progressSettle = 50 * time.Millisecond
	code := m.Run()
	if fakeDir != "" {
		_ = os.RemoveAll(fakeDir)
	}
	os.Exit(code)
}

// exeSuffix is what Windows needs on a program's name before it runs it.
var exeSuffix = map[bool]string{true: ".exe"}[runtime.GOOS == "windows"]

// fakePath builds the fake once and returns its path.
func fakePath(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		if fakeDir, fakeErr = os.MkdirTemp("", "lspindex-fake-"); fakeErr != nil {
			return
		}
		fakeBin = filepath.Join(fakeDir, "fakelsp"+exeSuffix)
		cmd := exec.Command("go", "build", "-o", fakeBin, "./testdata/fakelsp")
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeErr = fmt.Errorf("building the fake language server: %v\n%s", err, out)
		}
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	return fakeBin
}

// fakeScript mirrors the fake's script (see testdata/fakelsp/main.go).
type fakeScript struct {
	ServerInfo     map[string]string `json:"serverInfo,omitempty"`
	Record         string            `json:"record,omitempty"`
	Stderr         string            `json:"stderr,omitempty"`
	Messages       []map[string]any  `json:"messages,omitempty"`
	ServerRequests []string          `json:"serverRequests,omitempty"`
	Symbols        map[string]any    `json:"symbols,omitempty"`
	References     map[string][]loc  `json:"references,omitempty"`
	Implementation map[string][]loc  `json:"implementation,omitempty"`
	Fail           []string          `json:"fail,omitempty"`
	FailOnce       []string          `json:"failOnce,omitempty"`
	Hang           []string          `json:"hang,omitempty"`
	Die            string            `json:"die,omitempty"`
	Garbage        string            `json:"garbage,omitempty"`
	BadResult      []string          `json:"badResult,omitempty"`
	IgnoreExit     bool              `json:"ignoreExit,omitempty"`
	ProgressEndMs  int               `json:"progressEndMs,omitempty"`
	ProgressOpen   bool              `json:"progressOpen,omitempty"`
	Raw            map[string]any    `json:"raw,omitempty"`
}

// loc is a location in the script: a path relative to the root, an
// absolute path or a URI; lines are 0-based.
type loc struct {
	Path string `json:"path,omitempty"`
	URI  string `json:"uri,omitempty"`
	Line int    `json:"line"`
	Char int    `json:"char"`
}

// record is what the fake saw.
type record struct {
	Argv       []string          `json:"argv"`
	Pid        int               `json:"pid"`
	Initialize map[string]any    `json:"initialize"`
	Answers    []json.RawMessage `json:"answers"`
	Cancelled  []string          `json:"cancelled"`
	Asked      []string          `json:"asked"`
}

// sym builds a DocumentSymbol: span lines l1..l2 (0-based), name at
// selLine:selChar.
func sym(name string, kind, l1, l2, selLine, selChar int, children ...map[string]any) map[string]any {
	return map[string]any{
		"name": name, "kind": kind,
		"range":          map[string]any{"start": pos(l1, 0), "end": pos(l2, 200)},
		"selectionRange": map[string]any{"start": pos(selLine, selChar), "end": pos(selLine, selChar+len(name))},
		"children":       children,
	}
}

func pos(line, char int) map[string]int { return map[string]int{"line": line, "character": char} }

// fakeRun is one Index call against the fake.
type fakeRun struct {
	bin    string
	root   string
	script string
	rec    string
}

// newFakeRun writes the tree and the script; the fake is selected through
// the language's MDDB_LSP_* variable with the script path as its argument.
func newFakeRun(t *testing.T, tree map[string]string, sc fakeScript) *fakeRun {
	t.Helper()
	root := t.TempDir()
	for path, content := range tree {
		abs := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	f := &fakeRun{bin: fakePath(t), root: root, script: filepath.Join(dir, "script.json"), rec: filepath.Join(dir, "record.json")}
	sc.Record = f.rec
	data, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.script, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// options selects the fake for spec on top of opt.
func (f *fakeRun) options(spec ServerSpec, opt Options) Options {
	opt.Getenv = func(k string) string {
		if k == spec.EnvVar {
			return f.bin + " " + f.script
		}
		return ""
	}
	return opt
}

// record reads what the fake saw and checks its process is gone.
func (f *fakeRun) record(t *testing.T) record {
	t.Helper()
	data, err := os.ReadFile(f.rec)
	if err != nil {
		t.Fatalf("fake wrote no record: %v", err)
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	assertReaped(t, r.Pid)
	return r
}
