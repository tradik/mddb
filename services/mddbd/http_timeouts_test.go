package main

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #315. A handler that outlives the server's WriteTimeout still finishes its
// work, but its response is dropped: the client sees EOF and nothing is
// logged. Batch writes now lift the deadline for their own response, and
// every other request that misses it is logged.

func TestHTTPTimeoutsFromEnv(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{"", 30 * time.Second},
		{"2m", 2 * time.Minute},
		{"0", 0},
		{"soon", 30 * time.Second},
		{"-5s", 30 * time.Second},
	} {
		t.Setenv("MDDB_HTTP_WRITE_TIMEOUT", tc.raw)
		t.Setenv("MDDB_HTTP_READ_TIMEOUT", tc.raw)
		read, write := httpTimeouts()
		if read != tc.want || write != tc.want {
			t.Errorf("%q: read=%v write=%v, want %v", tc.raw, read, write, tc.want)
		}
	}
}

// serveOnce runs handler behind a real http.Server with the given write
// timeout and returns what a client received.
func serveOnce(t *testing.T, writeTimeout time.Duration, handler http.Handler) (string, error) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler, WriteTimeout: writeTimeout, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { _ = srv.Close() })

	resp, err := http.Post("http://"+lis.Addr().String()+"/v1/add-batch", "application/json", strings.NewReader("{}"))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func slowHandler(lift bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lift {
			liftWriteDeadline(w, r)
		}
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("done"))
	})
}

func TestLiftedWriteDeadlineDeliversASlowResponse(t *testing.T) {
	if _, err := serveOnce(t, 50*time.Millisecond, slowHandler(false)); err == nil {
		t.Fatal("control: a response past the write deadline was delivered, so this test proves nothing")
	}
	body, err := serveOnce(t, 50*time.Millisecond, withDeadlineWarning(slowHandler(true), 50*time.Millisecond))
	if err != nil || body != "done" {
		t.Errorf("lifted deadline: body=%q err=%v, want the response", body, err)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestAMissedWriteDeadlineIsLogged(t *testing.T) {
	logs := captureLog(t)
	h := withDeadlineWarning(slowHandler(false), 50*time.Millisecond)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/search", nil))

	out := logs.String()
	if !strings.Contains(out, "response not delivered") || !strings.Contains(out, "/v1/search") ||
		!strings.Contains(out, "MDDB_HTTP_WRITE_TIMEOUT") {
		t.Errorf("no usable warning for a missed deadline:\n%s", out)
	}
}

func TestNoWarningWhenTheDeadlineWasLiftedOrMet(t *testing.T) {
	logs := captureLog(t)
	withDeadlineWarning(slowHandler(true), 50*time.Millisecond).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/add-batch", nil))
	withDeadlineWarning(slowHandler(false), time.Second).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/search", nil))
	// No timeout configured: nothing can be missed.
	withDeadlineWarning(slowHandler(false), 0).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/search", nil))

	if strings.Contains(logs.String(), "response not delivered") {
		t.Errorf("warned without a missed deadline:\n%s", logs.String())
	}
}

// liftWriteDeadline must work behind middleware that does not install the
// warning state, e.g. in handler unit tests.
func TestLiftWriteDeadlineWithoutTheMiddleware(t *testing.T) {
	rec := httptest.NewRecorder()
	liftWriteDeadline(rec, httptest.NewRequest(http.MethodPost, "/", nil))
}
