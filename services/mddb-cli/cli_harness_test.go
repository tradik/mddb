package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// TEST-001. The CLI is the interface most people meet MDDB through, and it had
// no test that ran a command. These run the real command tree against a real
// HTTP server, so what is asserted is what a shell would see: the request that
// went out, the output that came back, and the exit path taken on failure.

// recordedRequest is what the fake server saw.
type recordedRequest struct {
	Method string
	Path   string
	Body   map[string]interface{}
	Header http.Header
}

// fakeServer answers CLI requests from a routing table and records every call.
type fakeServer struct {
	*httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
}

// newFakeServer routes by path. A route value may be a status code paired with
// a body, or just a body for 200.
func newFakeServer(t *testing.T, routes map[string]interface{}) *fakeServer {
	t.Helper()
	fs := &fakeServer{}

	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recordedRequest{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone()}
		if raw, err := io.ReadAll(r.Body); err == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &rec.Body)
		}
		fs.mu.Lock()
		fs.requests = append(fs.requests, rec)
		fs.mu.Unlock()

		// A path that answers differently per method — /v1/webhooks lists on
		// GET and registers on POST — is keyed as "METHOD path".
		route, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			route, ok = routes[r.URL.Path]
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no route in this test for ` + r.URL.Path + `"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch v := route.(type) {
		case failure:
			w.WriteHeader(v.status)
			_, _ = io.WriteString(w, v.body)
		case string:
			_, _ = io.WriteString(w, v)
		default:
			_ = json.NewEncoder(w).Encode(v)
		}
	}))
	t.Cleanup(fs.Close)
	return fs
}

// failure is a route that answers with an HTTP error.
type failure struct {
	status int
	body   string
}

func (fs *fakeServer) calls() []recordedRequest {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]recordedRequest(nil), fs.requests...)
}

// lastCall returns the most recent request, failing the test if there was none.
func (fs *fakeServer) lastCall(t *testing.T) recordedRequest {
	t.Helper()
	calls := fs.calls()
	if len(calls) == 0 {
		t.Fatal("the command sent no request")
	}
	return calls[len(calls)-1]
}

// runCLI executes one command against the given server and captures what it
// printed.
//
// Commands write with fmt.Println, which goes to os.Stdout and not to cobra's
// output writer, so the file descriptor itself is swapped for a pipe. Anything
// the command would have shown a user is what comes back here.
func runCLI(t *testing.T, serverAddr string, args ...string) (string, error) {
	t.Helper()
	return runCLIWithStdin(t, serverAddr, "", args...)
}

// runCLIWithStdin is runCLI for the commands that read a document from stdin.
func runCLIWithStdin(t *testing.T, serverAddr, stdin string, args ...string) (string, error) {
	t.Helper()

	origOut, origIn := os.Stdout, os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	if stdin != "" {
		inR, inW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			_, _ = io.WriteString(inW, stdin)
			_ = inW.Close()
		}()
		os.Stdin = inR
	}

	// A pipe holds 64 KiB before it blocks, and some commands print more than
	// that; drain it while the command runs.
	captured := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		captured <- string(out)
	}()

	root := newRootCmd()
	root.SetArgs(append([]string{"--server", serverAddr}, args...))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	// Cobra prints usage on a bad flag; that is the command's business, not
	// the harness's.
	root.SilenceUsage = true
	root.SilenceErrors = true

	runErr := root.Execute()

	_ = w.Close()
	os.Stdout, os.Stdin = origOut, origIn

	return <-captured, runErr
}

// wantsJSON is the shape of a body assertion: field path to expected value.
func assertBodyField(t *testing.T, body map[string]interface{}, field string, want interface{}) {
	t.Helper()
	got, present := body[field]
	if !present {
		t.Errorf("the request body has no %q field: %v", field, body)
		return
	}
	if gotStr, ok := got.(string); ok {
		if wantStr, ok := want.(string); ok && gotStr != wantStr {
			t.Errorf("%s = %q, want %q", field, gotStr, wantStr)
		}
		return
	}
	if !jsonEqual(got, want) {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func jsonEqual(a, b interface{}) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}

func mustContain(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("output does not mention %q:\n%s", want, out)
	}
}
