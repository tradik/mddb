package mddb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStatus(t *testing.T) {
	cases := []struct {
		name      string
		code      int
		body      string
		wantErr   string // substring; "" = no error
		wantReady bool
		check     func(t *testing.T, h Health)
	}{
		{
			name: "healthy", code: 200,
			body:      `{"status":"healthy","mode":"wr","durable":true,"persistence":{"path":"/data","writable":true},"mcpSessions":{"sse":1},"update":{"current":"2.18.2","available":false}}`,
			wantReady: true,
			check: func(t *testing.T, h Health) {
				if h.Mode != "wr" || !h.Durable || h.Persistence.Path != "/data" || h.MCPSessions["sse"] != 1 || h.Update.Current != "2.18.2" {
					t.Errorf("Health = %+v", h)
				}
			},
		},
		{
			name: "warming up with progress", code: 503,
			body: `{"status":"warming_up","phase":"open database","elapsedSeconds":42,"progress":{"bytesDone":50,"bytesTotal":100,"percent":50,"etaSeconds":40}}`,
			check: func(t *testing.T, h Health) {
				if h.Phase != "open database" || h.ElapsedSeconds != 42 || h.Progress == nil || h.Progress.Percent != 50 || h.Progress.EtaSeconds != 40 {
					t.Errorf("Health = %+v", h)
				}
			},
		},
		{
			name: "warming up without phase", code: 503, body: `{"status":"warming_up"}`,
			check: func(t *testing.T, h Health) {
				if h.Status != "warming_up" || h.Progress != nil {
					t.Errorf("Health = %+v", h)
				}
			},
		},
		{name: "unhealthy 503 is an error", code: 503, body: `{"status":"unhealthy","error":"disk"}`, wantErr: "(503)"},
		{name: "503 without JSON is an error", code: 503, body: `upstream down`, wantErr: "(503)"},
		{name: "500 is an error", code: 500, body: `{"status":"warming_up"}`, wantErr: "(500)"},
		{name: "malformed 200", code: 200, body: `{not json`, wantErr: "decode response"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			h, err := New(srv.URL).Status(context.Background())
			if path != "/health" {
				t.Errorf("hit %s, want /health", path)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if h.Ready != tc.wantReady {
				t.Errorf("Ready = %v, want %v", h.Ready, tc.wantReady)
			}
			if tc.check != nil {
				tc.check(t, h)
			}
		})
	}
}

func TestStatusConnectionError(t *testing.T) {
	if _, err := New("http://127.0.0.1:1").Status(context.Background()); err == nil {
		t.Error("expected connection error")
	}
}

// TestTimeouts checks that the client's default timeout bounds calls without
// a context deadline, and that a context deadline replaces it in both
// directions.
func TestTimeouts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(150 * time.Millisecond):
		case <-r.Context().Done():
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cases := []struct {
		name     string
		timeout  time.Duration
		deadline time.Duration // 0 = context without deadline
		wantErr  bool
	}{
		{"default timeout applies", 30 * time.Millisecond, 0, true},
		{"context deadline outlasts default", 30 * time.Millisecond, 5 * time.Second, false},
		{"context deadline shorter than default", time.Minute, 30 * time.Millisecond, true},
		{"zero disables the default", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.deadline > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.deadline)
				defer cancel()
			}
			_, err := New(srv.URL, WithTimeout(tc.timeout)).Do(ctx, "GET", "/slow", nil)
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestDefaultTimeout(t *testing.T) {
	c := New("http://x")
	if c.timeout != DefaultTimeout || c.httpc.Timeout != 0 {
		t.Errorf("timeout = %v, http.Client.Timeout = %v", c.timeout, c.httpc.Timeout)
	}
}
