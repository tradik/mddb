package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeVersionTools makes PATH hold only a gopls that prints its version and
// a phpactor that prints nothing; Python has no server.
func fakeVersionTools(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// The fake is a #!/bin/sh script, which Windows cannot run; the logic
		// under test does not depend on the platform and runs on Linux.
		t.Skip("shell-script fakes need a POSIX shell")
	}
	bin := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { // #nosec G306 -- a test program must be executable
			t.Fatal(err)
		}
	}
	write("gopls", `[ "$1" = version ] && echo "golang.org/x/tools/gopls v9.9.9"`)
	write("phpactor", "exit 0")
	t.Setenv("PATH", bin)
	for _, v := range []string{"MDDB_LSP_GO", "MDDB_LSP_PYTHON", "MDDB_LSP_PHP"} {
		t.Setenv(v, "")
	}
	return bin
}

func TestDiagnoseListsLanguageServers(t *testing.T) {
	bin := fakeVersionTools(t)
	t.Chdir(t.TempDir())
	srv := newFakeServer(t, map[string]interface{}{"/v1/admin/diagnose": string(testBundle(t, ""))})
	for _, args := range [][]string{{"diagnose"}, {"diagnose", "--inspect"}} {
		stdout, err := runCLI(t, srv.URL, args...)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Language servers (for autoindex --semantic):",
			"go      gopls: golang.org/x/tools/gopls v9.9.9", "php     phpactor (version unknown)", "python  not found"} {
			mustContain(t, stdout, want)
		}
		if strings.Contains(stdout, bin) {
			t.Errorf("%v shows where the servers are installed:\n%s", args, stdout)
		}
	}

	stdout, err := runCLI(t, srv.URL, "diagnose", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		LanguageServers []languageServer `json:"languageServers"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || len(res.LanguageServers) != 3 ||
		res.LanguageServers[0] != (languageServer{Language: "go", Found: true, Program: "gopls", Version: "golang.org/x/tools/gopls v9.9.9"}) {
		t.Fatalf("%q %v", stdout, err)
	}
	stdout, err = runCLI(t, srv.URL, "diagnose", "--inspect", "--json")
	if err != nil || strings.Contains(stdout, "Language servers") {
		t.Fatalf("--inspect --json prints the manifest alone: %q %v", stdout, err)
	}
}
