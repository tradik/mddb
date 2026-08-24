package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TEST-001. The rendering paths each command takes when the answer is not the
// happy one: an empty set, a disabled feature, --json, a browser that is not
// there. These are the branches a user hits on a real server and that nobody
// had ever run.

func TestVectorStatsSaysWhenEmbeddingIsOff(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/vector-stats": map[string]interface{}{"enabled": false},
	})

	out, err := runCLI(t, fs.URL, "vector-stats")
	if err != nil {
		t.Fatalf("vector-stats failed: %v", err)
	}
	// Silence would read as "no data"; the user needs to know it is switched
	// off and how to switch it on.
	mustContain(t, out, "disabled")
	mustContain(t, out, "MDDB_EMBEDDING_PROVIDER")
}

func TestVectorStatsRendersCoveragePerCollection(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/vector-stats": map[string]interface{}{
			"enabled": true, "provider": "openai", "model": "text-embedding-3-small",
			"dimensions": 1536, "index_ready": true,
			"collections": map[string]interface{}{
				"blog": map[string]interface{}{"total_documents": 200, "embedded_documents": 150},
				"docs": map[string]interface{}{"total_documents": 0, "embedded_documents": 0},
			},
		},
	})

	out, err := runCLI(t, fs.URL, "vector-stats")
	if err != nil {
		t.Fatalf("vector-stats failed: %v", err)
	}

	mustContain(t, out, "openai")
	mustContain(t, out, "75%") // 150 of 200
	// An empty collection must not divide by zero.
	mustContain(t, out, "docs")
	if strings.Contains(out, "NaN") || strings.Contains(out, "+Inf") {
		t.Errorf("an empty collection produced a non-number:\n%s", out)
	}
}

func TestVectorStatsWithNoCollectionsSaysSo(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/vector-stats": map[string]interface{}{"enabled": true, "provider": "openai"},
	})

	out, err := runCLI(t, fs.URL, "vector-stats")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No collections with embeddings")
}

func TestSchemaGetPrintsTheStoredSchema(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/schema/get": map[string]interface{}{
			"collection": "blog",
			"schema":     map[string]interface{}{"type": "object", "required": []string{"tag"}},
		},
	})

	out, err := runCLI(t, fs.URL, "schema", "get", "--collection", "blog")
	if err != nil {
		t.Fatalf("schema get failed: %v", err)
	}
	mustContain(t, out, "tag")
}

func TestSchemaDeleteConfirmsTheCollection(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/schema/delete": map[string]interface{}{"deleted": true},
	})

	out, err := runCLI(t, fs.URL, "schema", "delete", "--collection", "blog")
	if err != nil {
		t.Fatalf("schema delete failed: %v", err)
	}
	assertBodyField(t, fs.lastCall(t).Body, "collection", "blog")
	mustContain(t, out, "blog")
}

func TestSchemaListWithNoneSaysSo(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/schema/list": map[string]interface{}{"schemas": map[string]interface{}{}},
	})

	out, err := runCLI(t, fs.URL, "schema", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("schema list printed nothing at all for an empty database")
	}
}

// --json must be the server's bytes for every command that offers it: a script
// parsing one command's output should not have to special-case another's.
func TestJSONOutputIsUndecoratedEverywhere(t *testing.T) {
	cases := map[string]struct {
		route string
		body  string
		args  []string
	}{
		"stats":          {"/v1/stats", `{"mode":"wr"}`, []string{"stats"}},
		"vector-stats":   {"/v1/vector-stats", `{"enabled":false}`, []string{"vector-stats"}},
		"schema list":    {"/v1/schema/list", `{"schemas":{}}`, []string{"schema", "list"}},
		"webhook list":   {"GET /v1/webhooks", `[]`, []string{"webhook", "list"}},
		"vector-reindex": {"/v1/vector-reindex", `{"embedded":1}`, []string{"vector-reindex", "blog"}},
		"truncate":       {"/v1/truncate", `{"ok":true}`, []string{"truncate", "blog"}},
		"backup":         {"/v1/backup", `{"backup":"b.db"}`, []string{"backup", "b.db"}},
		"restore":        {"/v1/restore", `{"restored":"b.db"}`, []string{"restore", "b.db"}},
		"graphql":        {"/graphql", `{"data":{"x":1}}`, []string{"graphql", "{x}"}},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fs := newFakeServer(t, map[string]interface{}{c.route: c.body})

			out, err := runCLI(t, fs.URL, append([]string{"--json"}, c.args...)...)
			if err != nil {
				t.Fatalf("%s --json failed: %v", name, err)
			}

			got := strings.TrimSpace(out)
			if got != c.body {
				t.Errorf("--json printed %q, want the response verbatim (%q)", got, c.body)
			}
			var parsed interface{}
			if err := json.Unmarshal([]byte(got), &parsed); err != nil {
				t.Errorf("--json output is not parseable JSON: %v", err)
			}
		})
	}
}

// The playground opens a browser. On a machine without one it must say what to
// open by hand rather than failing.
func TestPlaygroundAlwaysTellsYouTheURL(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{})

	out, err := runCLI(t, fs.URL, "playground")
	if err != nil {
		t.Fatalf("playground returned an error: %v", err)
	}
	mustContain(t, out, fs.URL+"/playground")
}

func TestFileExistsAnswersBothWays(t *testing.T) {
	// A path that must exist on any machine running these tests.
	if !fileExists("/") {
		t.Error(`fileExists("/") = false`)
	}
	if fileExists("/definitely/not/here/" + t.Name()) {
		t.Error("fileExists reported a missing path as present")
	}
}

// --verbose writes a request trace to stderr; it must not contaminate stdout,
// which is what a pipe reads.
func TestVerboseDoesNotPolluteStdout(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{"/v1/export": `{"key":"a"}` + "\n"})

	out, err := runCLI(t, fs.URL, "--verbose", "export", "blog")
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"key":"a"}`+"\n" {
		t.Errorf("--verbose leaked into stdout: %q", out)
	}
}

// An unreachable server is the most common failure of all and must be an error,
// not an empty success.
func TestAnUnreachableServerIsAnError(t *testing.T) {
	for name, args := range map[string][]string{
		"stats":  {"stats"},
		"search": {"search", "blog"},
		"get":    {"get", "blog", "post", "en"},
	} {
		t.Run(name, func(t *testing.T) {
			// Port 1 is reserved and nothing listens there.
			if _, err := runCLI(t, "http://127.0.0.1:1", args...); err == nil {
				t.Error("a connection failure was reported as success")
			}
		})
	}
}

// Every command must exist under the name the manpage documents.
func TestTheAdvertisedCommandsAreAllRegistered(t *testing.T) {
	root := newRootCmd()

	registered := map[string]bool{}
	for _, c := range root.Commands() {
		registered[c.Name()] = true
	}

	for _, name := range []string{
		"add", "get", "search", "export", "backup", "restore", "truncate", "stats",
		"vector-search", "vector-reindex", "vector-stats", "import-url", "set-ttl",
		"fts", "webhook", "schema", "validate", "login", "api-key", "graphql", "playground",
	} {
		if !registered[name] {
			t.Errorf("command %q is documented and not registered", name)
		}
	}
}

func TestUnknownCommandsAreRejected(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{})

	if _, err := runCLI(t, fs.URL, "definitely-not-a-command"); err == nil {
		t.Error("an unknown command was accepted")
	}
}

// A subcommand group with no subcommand given must show its usage rather than
// doing something arbitrary.
func TestCommandGroupsWithoutASubcommand(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{})

	for _, group := range []string{"webhook", "schema", "api-key"} {
		t.Run(group, func(t *testing.T) {
			if _, err := runCLI(t, fs.URL, group); err != nil {
				// Cobra returns an error for a group with no Run; either
				// behaviour is fine as long as nothing was sent.
				_ = err
			}
			if calls := fs.calls(); len(calls) != 0 {
				t.Errorf("%q with no subcommand sent a request: %v", group, calls)
			}
		})
	}
}

func TestServerFlagDefaultsToLocalhost(t *testing.T) {
	root := newRootCmd()
	f := root.PersistentFlags().Lookup("server")
	if f == nil {
		t.Fatal("--server is not registered")
	}
	if f.DefValue != defaultServerURL {
		t.Errorf("--server defaults to %q, want %q", f.DefValue, defaultServerURL)
	}
	if serverURL != defaultServerURL {
		t.Errorf("a fresh command tree starts with serverURL = %q", serverURL)
	}
}

func TestHTTPErrorBodiesReachTheUser(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/stats": failure{http.StatusForbidden, `{"error":"admin access required"}`},
	})

	_, err := runCLI(t, fs.URL, "stats")
	if err == nil {
		t.Fatal("a 403 exited zero")
	}
	// The reason must survive to the terminal; "request failed" alone leaves
	// the user with nothing to act on.
	if !strings.Contains(err.Error(), "admin") && !strings.Contains(err.Error(), "403") {
		t.Errorf("the error says nothing useful: %v", err)
	}
}
