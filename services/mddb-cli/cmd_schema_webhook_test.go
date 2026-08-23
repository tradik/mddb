package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TEST-001. Schema, webhook and GraphQL commands. These take their arguments
// as flags rather than positionally, so the check that matters is that a
// missing required flag fails locally instead of reaching the server as an
// empty field.

func TestSchemaCommandsRequireTheirFlags(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/schema/set":    map[string]interface{}{"ok": true},
		"/v1/schema/get":    map[string]interface{}{"schema": "{}"},
		"/v1/schema/delete": map[string]interface{}{"ok": true},
		"/v1/validate":      map[string]interface{}{"valid": true},
	})

	for name, args := range map[string][]string{
		"set without a collection": {"schema", "set", "--schema", "{}"},
		"set without a schema":     {"schema", "set", "--collection", "blog"},
		"get without a collection": {"schema", "get"},
		"delete without one":       {"schema", "delete"},
		"validate without meta":    {"validate", "--collection", "blog"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runCLI(t, fs.URL, args...); err == nil {
				t.Error("the command ran with a required flag missing")
			}
		})
	}

	if calls := fs.calls(); len(calls) != 0 {
		t.Errorf("%d incomplete commands reached the server: %v", len(calls), calls)
	}
}

func TestSchemaSetSendsTheSchema(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/schema/set": map[string]interface{}{"ok": true},
	})

	const schema = `{"type":"object","required":["tag"]}`
	out, err := runCLI(t, fs.URL, "schema", "set", "--collection", "blog", "--schema", schema)
	if err != nil {
		t.Fatalf("schema set failed: %v", err)
	}

	body := fs.lastCall(t).Body
	assertBodyField(t, body, "collection", "blog")
	assertBodyField(t, body, "schema", schema)
	mustContain(t, out, "blog")
}

func TestSchemaListRendersTheCollections(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/schema/list": map[string]interface{}{
			"schemas": map[string]interface{}{
				"blog": map[string]interface{}{"type": "object"},
			},
		},
	})

	out, err := runCLI(t, fs.URL, "schema", "list")
	if err != nil {
		t.Fatalf("schema list failed: %v", err)
	}
	mustContain(t, out, "blog")
}

// --meta is JSON here, not the key=value syntax the other commands use; a
// malformed value must be reported as such before anything is sent.
func TestValidateRejectsMalformedJSONLocally(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/validate": map[string]interface{}{"valid": true},
	})

	_, err := runCLI(t, fs.URL, "validate", "--collection", "blog", "--meta", "{not json")
	if err == nil {
		t.Fatal("malformed --meta JSON was accepted")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "json") {
		t.Errorf("the error does not name the problem: %v", err)
	}
	if calls := fs.calls(); len(calls) != 0 {
		t.Errorf("malformed JSON reached the server: %v", calls)
	}
}

func TestValidateReportsBothOutcomes(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		fs := newFakeServer(t, map[string]interface{}{
			"/v1/validate": map[string]interface{}{"valid": true},
		})
		out, err := runCLI(t, fs.URL, "validate", "--collection", "blog",
			"--meta", `{"tag":["go"]}`)
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, out, "valid")
	})

	t.Run("invalid", func(t *testing.T) {
		fs := newFakeServer(t, map[string]interface{}{
			"/v1/validate": map[string]interface{}{
				"valid":  false,
				"errors": []string{"tag is required"},
			},
		})
		out, err := runCLI(t, fs.URL, "validate", "--collection", "blog", "--meta", `{}`)
		if err != nil {
			t.Fatalf("a validation failure is an answer, not an error: %v", err)
		}
		// The reason must be shown; "failed" alone leaves the user guessing.
		mustContain(t, out, "tag is required")
	})
}

func TestWebhookRegisterRequiresURLAndEvents(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/webhooks": map[string]interface{}{"id": "wh_1"},
	})

	for name, args := range map[string][]string{
		"no url":    {"webhook", "register", "--events", "doc.added"},
		"no events": {"webhook", "register", "--url", "https://x.test/hook"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runCLI(t, fs.URL, args...); err == nil {
				t.Error("the webhook was registered with a required flag missing")
			}
		})
	}
	if calls := fs.calls(); len(calls) != 0 {
		t.Errorf("an incomplete registration reached the server: %v", calls)
	}
}

func TestWebhookRegisterSplitsItsEvents(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/webhooks": map[string]interface{}{"id": "wh_1", "url": "https://x.test/hook"},
	})

	out, err := runCLI(t, fs.URL, "webhook", "register",
		"--url", "https://x.test/hook",
		"--events", "doc.added,doc.updated",
		"--collection", "blog")
	if err != nil {
		t.Fatalf("webhook register failed: %v", err)
	}

	body := fs.lastCall(t).Body
	assertBodyField(t, body, "url", "https://x.test/hook")
	assertBodyField(t, body, "events", []string{"doc.added", "doc.updated"})
	assertBodyField(t, body, "collection", "blog")
	mustContain(t, out, "wh_1")
}

// Without --collection the hook fires for every collection, so the field must
// be absent rather than sent as an empty string a server might match on.
func TestWebhookRegisterOmitsAnUnsetCollection(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/webhooks": map[string]interface{}{"id": "wh_1"},
	})

	if _, err := runCLI(t, fs.URL, "webhook", "register",
		"--url", "https://x.test/hook", "--events", "doc.added"); err != nil {
		t.Fatal(err)
	}
	if _, present := fs.lastCall(t).Body["collection"]; present {
		t.Error("an unset --collection was sent as a value")
	}
}

func TestWebhookListAndDelete(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		// The server answers GET /v1/webhooks with a bare array, not an
		// object wrapping one.
		"GET /v1/webhooks": []map[string]interface{}{
			{"id": "wh_1", "url": "https://x.test/hook", "events": []string{"doc.added"}},
		},
		"/v1/webhooks/delete": map[string]interface{}{"deleted": true},
	})

	out, err := runCLI(t, fs.URL, "webhook", "list")
	if err != nil {
		t.Fatalf("webhook list failed: %v", err)
	}
	mustContain(t, out, "wh_1")

	if _, err := runCLI(t, fs.URL, "webhook", "delete", "wh_1"); err != nil {
		t.Fatalf("webhook delete failed: %v", err)
	}
	assertBodyField(t, fs.lastCall(t).Body, "id", "wh_1")
}

func TestGraphQLSendsQueryAndVariables(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/graphql": map[string]interface{}{
			"data": map[string]interface{}{"stats": map[string]interface{}{"totalDocuments": 7}},
		},
	})

	out, err := runCLI(t, fs.URL, "graphql", `query($c:String!){ documents(collection:$c){ key } }`,
		"--variables", `{"c":"blog"}`)
	if err != nil {
		t.Fatalf("graphql failed: %v", err)
	}

	body := fs.lastCall(t).Body
	if !strings.Contains(body["query"].(string), "documents") {
		t.Errorf("query = %v", body["query"])
	}
	assertBodyField(t, body, "variables", map[string]string{"c": "blog"})

	// The response is pretty-printed so a human can read it.
	mustContain(t, out, "totalDocuments")
}

func TestGraphQLRejectsMalformedVariables(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/graphql": map[string]interface{}{"data": nil},
	})

	if _, err := runCLI(t, fs.URL, "graphql", "{ stats { totalDocuments } }",
		"--variables", "not json"); err == nil {
		t.Fatal("malformed --variables was accepted")
	}
	if calls := fs.calls(); len(calls) != 0 {
		t.Errorf("malformed variables reached the server: %v", calls)
	}
}

// GraphQL answers 200 with an errors array; the CLI must show it rather than
// printing an empty data block.
func TestGraphQLShowsServerErrors(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/graphql": map[string]interface{}{
			"data":   nil,
			"errors": []map[string]interface{}{{"message": "unknown field stats2"}},
		},
	})

	out, err := runCLI(t, fs.URL, "graphql", "{ stats2 }")
	if err != nil {
		t.Fatalf("graphql failed: %v", err)
	}
	mustContain(t, out, "unknown field stats2")
}

func TestGraphQLSurfacesTransportFailures(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/graphql": failure{http.StatusBadGateway, `{"error":"upstream down"}`},
	})

	if _, err := runCLI(t, fs.URL, "graphql", "{ stats { totalDocuments } }"); err == nil {
		t.Fatal("a 502 was reported as success")
	}
}

// TEST-001 found this: "schema set" defined -s for --schema while the root
// command uses -s for --server, and pflag panics when a subcommand redefines
// an inherited shorthand. Every invocation crashed with a stack trace, --help
// included, so the command had never worked.
func TestNoSubcommandRedefinesAnInheritedShorthand(t *testing.T) {
	root := newRootCmd()

	persistent := map[string]string{}
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Shorthand != "" {
			persistent[f.Shorthand] = f.Name
		}
	})
	if len(persistent) == 0 {
		t.Fatal("the root command has no persistent shorthands — this test would prove nothing")
	}

	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Shorthand == "" {
				return
			}
			if global, taken := persistent[f.Shorthand]; taken && f.Name != global {
				t.Errorf("%s: -%s is --%s locally and --%s globally; pflag panics on this",
					path, f.Shorthand, f.Name, global)
			}
		})
		for _, sub := range c.Commands() {
			walk(sub, path+" "+sub.Name())
		}
	}
	walk(root, "mddb-cli")
}

// Help must render for every command: a panic here is what hid the shorthand
// collision, since nobody reaches a command whose --help crashes.
func TestEveryCommandCanRenderItsHelp(t *testing.T) {
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		t.Run(path, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s --help panicked: %v", path, r)
				}
			}()
			var sb strings.Builder
			c.SetOut(&sb)
			if err := c.Usage(); err != nil {
				t.Errorf("usage: %v", err)
			}
			if sb.Len() == 0 {
				t.Error("the command printed no usage")
			}
		})
		for _, sub := range c.Commands() {
			walk(sub, path+"_"+sub.Name())
		}
	}
	walk(newRootCmd(), "mddb-cli")
}

// An empty list is a normal answer and must say so.
func TestWebhookListWithNoneRegistered(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"GET /v1/webhooks": []map[string]interface{}{},
	})

	out, err := runCLI(t, fs.URL, "webhook", "list")
	if err != nil {
		t.Fatalf("webhook list failed: %v", err)
	}
	mustContain(t, out, "No webhooks registered")
}
