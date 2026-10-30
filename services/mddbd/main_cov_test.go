package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"mddb/internal/compression"
	"mddb/internal/geo"
	"mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// ---------------------------------------------------------------- helpers

// covABaseEnv makes startup deterministic and offline: no update check, no
// local-model probe, no provider from the developer's environment.
func covABaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MDDB_UPDATE_CHECK", "0")
	t.Setenv("MDDB_EMBEDDING_AUTODETECT", "0")
	t.Setenv("MDDB_EMBEDDING_PROVIDER", "")
	t.Setenv("MDDB_STORAGE_LAYOUT", "")
	t.Setenv("MDDB_ENCRYPTION_KEY", "")
	t.Setenv("MDDB_CORS_ORIGINS", "")
	t.Setenv("MDDB_CORS_ORIGIN", "")
	t.Setenv(mcpProtocolVersionEnv, "")
	prevEnc := globalEncryptor
	t.Cleanup(func() {
		SetGlobalEncryptor(prevEnc)
		compression.ConfigureCompression(true, 1024, 10*1024)
	})
}

// covAFreeAddr returns a loopback TCP address nothing is listening on.
func covAFreeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// covABusyAddr returns a loopback address held by a listener for the rest of
// the test.
func covABusyAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l.Addr().String()
}

// covAConfig is a configuration with every listener off and a database in a
// fresh directory.
func covAConfig(t *testing.T) ServerConfig {
	t.Helper()
	cfg := defaultServerConfig()
	cfg.Database.Path = filepath.Join(t.TempDir(), "mddb.db")
	cfg.HTTP.Enabled = false
	cfg.GRPC.Enabled = false
	cfg.MCP.Enabled = false
	cfg.HTTP3.Enabled = false
	return cfg
}

// covAFatalCall is one call to the fatal seam.
type covAFatalCall struct {
	msg  string
	args string
}

// covATrapFatal replaces fatal with a function that reports the call and
// ends only the calling goroutine, as the real one ends the process.
func covATrapFatal(t *testing.T) <-chan covAFatalCall {
	t.Helper()
	ch := make(chan covAFatalCall, 8)
	prev := fatal
	fatal = func(msg string, args ...any) {
		ch <- covAFatalCall{msg: msg, args: fmt.Sprint(args...)}
		runtime.Goexit()
	}
	t.Cleanup(func() { fatal = prev })
	return ch
}

// covAExpectFatal runs fn and returns the fatal call it ends in — made by fn
// itself or by a goroutine it started.
func covAExpectFatal(t *testing.T, fn func()) covAFatalCall {
	t.Helper()
	ch := covATrapFatal(t)
	go fn()
	select {
	case c := <-ch:
		return c
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for a fatal startup error")
	}
	return covAFatalCall{}
}

// covAWaitLog polls the captured log until it contains want.
func covAWaitLog(t *testing.T, logs *covALogBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(logs.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("log never contained %q:\n%s", want, logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// covAInitServer opens cfg's database and runs every startup step up to the
// listeners, in runServer's order, stopping everything again when the test
// ends.
func covAInitServer(t *testing.T, cfg ServerConfig) *Server {
	t.Helper()
	db, fl := openServerDatabase(newStartupClock(), cfg.Database.Path)
	s := newServer(cfg, db, CheckPersistence(cfg.Database.Path), fl)
	s.checkCollectionLayout()
	s.initSubsystems(newStartupClock())
	t.Cleanup(func() { covAStopServer(s) })
	return s
}

// covAStopServer stops what initSubsystems started and closes the database.
func covAStopServer(s *Server) {
	s.AuditManager.Stop()
	s.DiskMonitor.Stop()
	if s.AutomationLogStore != nil {
		s.AutomationLogStore.Stop()
	}
	if s.Binlog != nil {
		_ = s.Binlog.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.Shutdown(ctx)
}

// covARunning is a runServer call in progress.
type covARunning struct {
	sig  chan os.Signal
	done chan int
	logs *covALogBuffer
}

// covAStartServer runs runServer(cfg) in the background with a test signal
// channel and waits until it reports ready.
func covAStartServer(t *testing.T, cfg ServerConfig) *covARunning {
	t.Helper()
	logs := covACaptureLogs(t)
	r := &covARunning{sig: make(chan os.Signal, 1), done: make(chan int, 1), logs: logs}

	prevSignals, prevExit := shutdownSignals, shutdownExit
	shutdownSignals = func() chan os.Signal { return r.sig }
	shutdownExit = func(code int) { t.Errorf("shutdown did not finish (exit %d)", code) }
	t.Cleanup(func() { shutdownSignals, shutdownExit = prevSignals, prevExit })
	calls := covATrapFatal(t)

	go func() { r.done <- runServer(cfg) }()
	deadline := time.After(30 * time.Second)
	for !strings.Contains(logs.String(), "ready to serve") {
		select {
		case c := <-calls:
			t.Fatalf("startup failed: %s %s", c.msg, c.args)
		case <-deadline:
			t.Fatalf("server never became ready:\n%s", logs.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	return r
}

// stop delivers SIGTERM and returns runServer's exit code.
func (r *covARunning) stop(t *testing.T) int {
	t.Helper()
	r.sig <- syscall.SIGTERM
	select {
	case code := <-r.done:
		return code
	case <-time.After(30 * time.Second):
		t.Fatalf("server did not shut down:\n%s", r.logs.String())
	}
	return -1
}

// covAGet fetches url without keeping the connection alive.
func covAGet(t *testing.T, url string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := client.Get(url) // #nosec G107 -- test URL on loopback
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return resp.StatusCode, string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s: %v", url, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---------------------------------------------------------------- lifecycle

// Every optional subsystem on: the server starts, serves /health over HTTP,
// reports each subsystem it started, and shuts down cleanly with exit code 0.
func TestCovARunServerEverythingEnabled(t *testing.T) {
	covABaseEnv(t)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer hook.Close()

	t.Setenv("MDDB_AUDIT_ENABLED", "true")
	t.Setenv("MDDB_AUDIT_RETENTION_DAYS", "30")
	t.Setenv("MDDB_AUDIT_EXPORT_BUFFER", "16")
	t.Setenv("MDDB_AUDIT_EXPORT_WEBHOOK_URL", hook.URL)
	t.Setenv("MDDB_AUDIT_EXPORT_SYSLOG_ADDR", "udp://127.0.0.1:9")
	t.Setenv("MDDB_TEMPORAL", "true")
	t.Setenv("MDDB_SPELL", "true")
	t.Setenv("MDDB_CRONS", "true")
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	t.Setenv("MDDB_AUTH_JWT_SECRET", "covA-secret-0123456789abcdef0123456789")
	t.Setenv("MDDB_AUTH_ADMIN_PASSWORD", "covA-Admin-Passw0rd!")
	t.Setenv("MDDB_REPLICATION_ROLE", "leader")
	t.Setenv("MDDB_ACCESS_LOG", "true")
	t.Setenv("MDDB_RATE_LIMIT_ENABLED", "true")
	t.Setenv("MDDB_EMBEDDING_PROVIDER", "ollama")
	t.Setenv("MDDB_EMBEDDING_API_URL", "http://127.0.0.1:9")
	t.Setenv(mcpProtocolVersionEnv, supportedMCPVersions[0])

	cfg := covAConfig(t)
	cfg.HTTP.Enabled, cfg.HTTP.Addr, cfg.HTTP.Mode = true, covAFreeAddr(t), ModeRW
	cfg.GRPC.Enabled, cfg.GRPC.Addr, cfg.GRPC.Mode = true, "127.0.0.1:0", ModeRW
	cfg.MCP.Enabled, cfg.MCP.Addr, cfg.MCP.Mode = true, "127.0.0.1:0", ModeRW
	cfg.HTTP3.Enabled, cfg.HTTP3.Addr, cfg.HTTP3.Mode = true, "127.0.0.1:0", ModeRW
	cfg.Compression.Enabled = false
	cfg.Vector.BQRerankFactor = 0

	r := covAStartServer(t, cfg)

	code, body := covAGet(t, "http://"+cfg.HTTP.Addr+"/health")
	if code != http.StatusOK || !strings.Contains(body, "healthy") {
		t.Fatalf("/health = %d %s, want 200 healthy", code, body)
	}
	covAWaitLog(t, r.logs, "mddb gRPC listening")
	covAWaitLog(t, r.logs, "mddb MCP HTTP listening")
	covAWaitLog(t, r.logs, "mddb HTTP listening")

	if got := r.stop(t); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	logs := r.logs.String()
	for _, want := range []string{
		"Extreme Performance Mode ENABLED",
		"Audit webhook exporter active",
		"Audit syslog exporter active",
		"Document compression disabled",
		"Temporal event tracking initialized",
		"Spell manager initialized",
		"Cron scheduler started",
		"Authentication enabled",
		"binlog enabled",
		"replication leader started",
		"access logging enabled",
		"CORS is wildcard (*) with auth enabled",
		"MCP protocol revision is pinned",
		"mddb HTTP/3 listening",
		"Per-protocol mode HTTP3",
		"source=environment",
		"Received, shutting down",
		"shutdown complete",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log is missing %q", want)
		}
	}
	dir := filepath.Dir(cfg.Database.Path)
	for _, f := range []string{"mddb.wal", "mddb.binlog"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s was not created: %v", f, err)
		}
	}
}

// The other side of every switch: a follower with automations disabled and a
// binlog (which used to crash on the missing automation manager), an
// autodetected local model, external panel, and every listener off.
func TestCovARunServerFollowerWithEverythingOff(t *testing.T) {
	covABaseEnv(t)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"nomic-embed-text:latest"}]}`))
	}))
	defer ollama.Close()

	t.Setenv("MDDB_EMBEDDING_AUTODETECT", "1")
	t.Setenv("OLLAMA_HOST", ollama.URL)
	t.Setenv("MDDB_REPLICATION_ROLE", "follower")
	t.Setenv("MDDB_REPLICATION_LEADER_ADDR", covAFreeAddr(t))
	t.Setenv("MDDB_AUTOMATIONS", "disable")
	t.Setenv("MDDB_BINLOG_ENABLED", "true")
	t.Setenv("MDDB_PANEL_MODE", "external")
	t.Setenv("MDDB_GRAPHQL_ENABLED", "false")
	t.Setenv("MDDB_METRICS", "false")
	t.Setenv("MDDB_SSE_ENABLED", "false")

	cfg := covAConfig(t)
	cfg.FTS.StemmingEnabled = false
	cfg.FTS.SynonymsEnabled = false

	r := covAStartServer(t, cfg)
	if got := r.stop(t); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	logs := r.logs.String()
	for _, want := range []string{
		"source=autodetected",
		"Replication follower mode",
		"binlog enabled",
		"Replication disabled TTL cleanup and embedding worker on follower",
		"Replication client started",
		"Panel mode external",
		"HTTP server disabled",
		"MCP server disabled",
		"gRPC server disabled",
		"shutdown complete",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log is missing %q", want)
		}
	}
	for _, unwanted := range []string{"GraphQL endpoint enabled", "Prometheus metrics enabled", "SSE event stream enabled", "automation manager initialized"} {
		if strings.Contains(logs, unwanted) {
			t.Errorf("log has %q although it was disabled", unwanted)
		}
	}
}

// run reads the configuration through the seam and, in MCP stdio mode, returns
// 0 when stdin closes instead of starting any listener.
func TestCovARunMCPStdio(t *testing.T) {
	covABaseEnv(t)
	logs := covACaptureLogs(t)
	cfg := covAConfig(t)
	cfg.MCP.Stdio = true
	prevLoad := loadConfig
	loadConfig = func() ServerConfig { return cfg }
	t.Cleanup(func() { loadConfig = prevLoad })
	covATrapFatal(t)

	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = wr.Close()
	prevIn := os.Stdin
	os.Stdin = rd
	t.Cleanup(func() { os.Stdin = prevIn; _ = rd.Close() })

	if got := run(); got != 0 {
		t.Fatalf("run() = %d, want 0", got)
	}
	_ = logs
	if _, err := os.Stat(cfg.Database.Path); err != nil {
		t.Fatalf("stdio mode should still open the database: %v", err)
	}
}

// A process started as the integrity-check child checks the file and exits
// with its verdict, before any server setup.
func TestCovARunVerifyChild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ok.db")
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	t.Setenv(verifyChildEnv, path)
	prevLoad := loadConfig
	loadConfig = func() ServerConfig {
		t.Error("the verify child must not load the server configuration")
		return ServerConfig{}
	}
	t.Cleanup(func() { loadConfig = prevLoad })
	if got := run(); got != 0 {
		t.Fatalf("verify child on a healthy file = %d, want 0", got)
	}
}

// ---------------------------------------------------------------- seams

func TestCovAIgnoreServerClosed(t *testing.T) {
	if err := ignoreServerClosed(http.ErrServerClosed); err != nil {
		t.Fatalf("a deliberate close is not a failure, got %v", err)
	}
	boom := errors.New("boom")
	if err := ignoreServerClosed(boom); !errors.Is(err, boom) {
		t.Fatalf("other errors pass through, got %v", err)
	}
}

func TestCovAClientAuthMode(t *testing.T) {
	if got := clientAuthMode(TLSConfig{}); got != "require" {
		t.Fatalf("default = %q, want require", got)
	}
	if got := clientAuthMode(TLSConfig{ClientAuth: "request"}); got != "request" {
		t.Fatalf("explicit = %q, want request", got)
	}
}

func TestCovAWarnSlowOpen(t *testing.T) {
	logs := covACaptureLogs(t)
	warnSlowOpen(false, time.Hour)
	warnSlowOpen(true, time.Second)
	if strings.Contains(logs.String(), "walked every page") {
		t.Fatal("only a walk longer than 30s is worth a warning")
	}
	warnSlowOpen(true, 31*time.Second)
	if !strings.Contains(logs.String(), "walked every page") || !strings.Contains(logs.String(), "elapsed=31s") {
		t.Fatalf("slow walk not reported:\n%s", logs.String())
	}
}

// ---------------------------------------------------------------- startup failures

func TestCovAFatalDatabaseUnopenable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.db")
	if err := os.WriteFile(path, []byte(strings.Repeat("not a bolt file ", 512)), 0o600); err != nil {
		t.Fatal(err)
	}
	c := covAExpectFatal(t, func() { openServerDatabase(newStartupClock(), path) })
	if c.msg != "startup step failed" || !strings.Contains(c.args, "bolt.Open") || !strings.Contains(c.args, "-repair-db") {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestCovAFatalPerCollectionLayoutWithDataInMainFile(t *testing.T) {
	covABaseEnv(t)
	s := covAInitServer(t, covAConfig(t))
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put([]byte("k"), []byte("v"))
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	c := covAExpectFatal(t, s.checkCollectionLayout)
	if !strings.Contains(c.args, "per-collection layout") || !strings.Contains(c.args, "-split-collections") {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestCovAPerCollectionLayout(t *testing.T) {
	covABaseEnv(t)
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	covATrapFatal(t)
	s := covAInitServer(t, covAConfig(t)) // an empty main file passes and opens the directory
	if s.collectionStore() == nil {
		t.Fatal("per-collection files should be open")
	}
	if _, err := os.Stat(collectionDir(s.Path)); err != nil {
		t.Fatalf("collection directory not created: %v", err)
	}

	blocked := filepath.Join(t.TempDir(), "blocked.db")
	db, err := bolt.Open(blocked, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := os.WriteFile(collectionDir(blocked), []byte("a file, not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := covAExpectFatal(t, (&Server{DB: db, Path: blocked}).checkCollectionLayout)
	if !strings.Contains(c.args, "opening per-collection files") {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestCovAFatalAuthMisconfigured(t *testing.T) {
	covABaseEnv(t)
	s := covAInitServer(t, covAConfig(t))
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	t.Setenv("MDDB_AUTH_JWT_SECRET", "")
	c := covAExpectFatal(t, func() { s.initAuth(newStartupClock()) })
	if !strings.Contains(c.msg, "requires MDDB_AUTH_JWT_SECRET") {
		t.Fatalf("fatal = %+v", c)
	}

	t.Setenv("MDDB_AUTH_JWT_SECRET", "covA-secret")
	t.Setenv("MDDB_AUTH_JWT_EXPIRY", "a fortnight")
	c = covAExpectFatal(t, func() { s.initAuth(newStartupClock()) })
	if c.msg != "Invalid MDDB_AUTH_JWT_EXPIRY" {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestCovAFatalAutomationLogTTL(t *testing.T) {
	covABaseEnv(t)
	s := covAInitServer(t, covAConfig(t))
	t.Setenv("MDDB_AUTOMATION_LOGS_TTL", "forever")
	c := covAExpectFatal(t, func() { s.initAutomation() })
	if c.msg != "Invalid MDDB_AUTOMATION_LOGS_TTL" {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestCovAAutomationLogsDisabled(t *testing.T) {
	covABaseEnv(t)
	t.Setenv("MDDB_AUTOMATION_LOGS", "disable")
	s := covAInitServer(t, covAConfig(t))
	if s.AutomationManager == nil || s.AutomationLogStore != nil {
		t.Fatalf("manager %v, log store %v: want a manager without logs", s.AutomationManager, s.AutomationLogStore)
	}
}

func TestCovAEncryption(t *testing.T) {
	covABaseEnv(t)
	s := covAInitServer(t, covAConfig(t))
	if err := s.CollectionManager.Set("secret", &CollectionConfig{Encrypted: true}); err != nil {
		t.Fatal(err)
	}

	// Opted-in collection without a key: refuse to start.
	c := covAExpectFatal(t, s.initEncryption)
	if !strings.Contains(c.msg, "encrypted=true but MDDB_ENCRYPTION_KEY is not set") || !strings.Contains(c.args, "secret") {
		t.Fatalf("fatal = %+v", c)
	}

	// A key that is not a key: refuse to start.
	t.Setenv("MDDB_ENCRYPTION_KEY", "not base64 at all!")
	c = covAExpectFatal(t, s.initEncryption)
	if c.msg != "encryption init" {
		t.Fatalf("fatal = %+v", c)
	}

	// A valid key: encryption on, rotation available.
	t.Setenv("MDDB_ENCRYPTION_KEY", genKey(t))
	covATrapFatal(t)
	s.initEncryption()
	if !s.Encryptor.Enabled() || s.RotationManager == nil {
		t.Fatal("a valid key must enable encryption and the rotation manager")
	}
}

func TestCovAFatalFollowerWithoutLeader(t *testing.T) {
	t.Setenv("MDDB_REPLICATION_LEADER_ADDR", "")
	s := &Server{ReplicationRole: "follower"}
	c := covAExpectFatal(t, func() { s.startReplicationClient() })
	if !strings.Contains(c.msg, "requires MDDB_REPLICATION_LEADER_ADDR") {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestCovAFatalUnknownMCPRevision(t *testing.T) {
	t.Setenv(mcpProtocolVersionEnv, "1999-01-01")
	s := &Server{Config: ServerConfig{MCP: MCPConfig{Enabled: true}}}
	c := covAExpectFatal(t, func() { s.startMCPListener("internal", time.Second, time.Second) })
	if !strings.Contains(c.args, "MCP protocol version") {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestCovAFatalHTTPListener(t *testing.T) {
	// The address is taken.
	s := &Server{Config: ServerConfig{HTTP: HTTPConfig{Enabled: true, Addr: covABusyAddr(t)}}}
	c := covAExpectFatal(t, func() { s.serveHTTP(http.NotFoundHandler(), time.Second, time.Second) })
	if !strings.Contains(c.args, "openListener") {
		t.Fatalf("fatal = %+v", c)
	}

	// TLS is on but the key pair does not exist.
	missing := filepath.Join(t.TempDir(), "missing.pem")
	s = &Server{Config: ServerConfig{
		HTTP: HTTPConfig{Enabled: true, Addr: "127.0.0.1:0"},
		TLS:  TLSConfig{Enabled: true, CertFile: missing, KeyFile: missing},
	}}
	c = covAExpectFatal(t, func() { s.serveHTTP(http.NotFoundHandler(), time.Second, time.Second) })
	if c.msg != "TLS config" {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestCovAServeHTTPS(t *testing.T) {
	logs := covACaptureLogs(t)
	covATrapFatal(t)
	dir := t.TempDir()
	cert, key := makeTestKeyPair(t, dir, "server")
	ca, _ := makeTestKeyPair(t, dir, "ca")
	addr := covAFreeAddr(t)
	s := &Server{Path: "x.db", Mode: ModeRW, Config: ServerConfig{
		HTTP: HTTPConfig{Enabled: true, Addr: addr},
		TLS:  TLSConfig{Enabled: true, CertFile: cert, KeyFile: key, ClientCAFile: ca, ClientAuth: "request"},
	}}
	s.startHTTPListener(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tls-ok"))
	}), time.Second, time.Second)

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, // #nosec G402 -- self-signed test cert
	}}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Get("https://" + addr + "/")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if string(b) != "tls-ok" {
				t.Fatalf("body = %q", b)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("HTTPS never answered: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "clientAuth=request") {
		t.Fatalf("mTLS mode not logged:\n%s", logs.String())
	}
}

func TestCovAServeHTTPIgnoresTLSOnUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("MDDB refuses unix socket listeners on Windows by design")
	}
	logs := covACaptureLogs(t)
	covATrapFatal(t)
	sock := filepath.Join(t.TempDir(), "m.sock")
	s := &Server{Config: ServerConfig{
		HTTP: HTTPConfig{Enabled: true, Addr: "unix:" + sock},
		TLS:  TLSConfig{Enabled: true, CertFile: "c", KeyFile: "k"},
	}}
	s.startHTTPListener(http.NotFoundHandler(), time.Second, time.Second)
	covAWaitLog(t, logs, "mddb HTTP listening")
	if !strings.Contains(logs.String(), "TLS ignored on UDS listener") {
		t.Fatalf("UDS TLS skip not logged:\n%s", logs.String())
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket not created: %v", err)
	}
}

func TestCovAFatalMCPListenerAddressTaken(t *testing.T) {
	covABaseEnv(t)
	cfg := covAConfig(t)
	cfg.MCP.Enabled, cfg.MCP.Addr = true, covABusyAddr(t)
	s := covAInitServer(t, cfg)
	c := covAExpectFatal(t, func() { s.startMCPListener("internal", time.Second, time.Second) })
	if !strings.Contains(c.args, "MCP listen") {
		t.Fatalf("fatal = %+v", c)
	}
	if s.mcpSSE == nil || s.mcpStreamable == nil {
		t.Fatal("transports must exist before the listener goroutine starts")
	}
}

func TestCovAFatalGRPCListener(t *testing.T) {
	covABaseEnv(t)
	cfg := covAConfig(t)
	cfg.GRPC.Enabled, cfg.GRPC.Addr = true, covABusyAddr(t)
	s := covAInitServer(t, cfg)
	c := covAExpectFatal(t, func() { s.startGRPCListener(false) })
	if !strings.Contains(c.args, "startGRPCServer") {
		t.Fatalf("fatal = %+v", c)
	}

	missing := filepath.Join(t.TempDir(), "missing.pem")
	s.Config.TLS = TLSConfig{Enabled: true, CertFile: missing, KeyFile: missing}
	c = covAExpectFatal(t, func() { s.startGRPCListener(false) })
	if c.msg != "gRPC TLS config" {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestCovAGRPCServerOptions(t *testing.T) {
	covABaseEnv(t)
	t.Setenv("MDDB_RATE_LIMIT_ENABLED", "true")
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	t.Setenv("MDDB_AUTH_JWT_SECRET", "covA-secret-0123456789abcdef0123456789")
	dir := t.TempDir()
	cert, key := makeTestKeyPair(t, dir, "server")
	cfg := covAConfig(t)
	cfg.GRPC.Addr = "127.0.0.1:0"
	cfg.TLS = TLSConfig{Enabled: true, CertFile: cert, KeyFile: key, ClientCAFile: cert}
	s := covAInitServer(t, cfg)
	covATrapFatal(t)

	opts, note := s.grpcServerOptions(true)
	// rate limit + auth chains (unary, stream) and TLS credentials.
	if len(opts) != 3 || note != ", tls=on, mtls=on (clientAuth=require)" {
		t.Fatalf("opts = %d, note = %q", len(opts), note)
	}

	s.Config.TLS.ClientCAFile = ""
	if _, note = s.grpcServerOptions(false); note != ", tls=on" {
		t.Fatalf("TLS without mTLS note = %q", note)
	}

	s.Config.GRPC.Addr = "unix:" + filepath.Join(dir, "g.sock")
	if _, note = s.grpcServerOptions(false); note != "" {
		t.Fatalf("a Unix socket takes no TLS, note = %q", note)
	}
}

// ---------------------------------------------------------------- steps

func TestCovAInitEmbeddingFromStoredConfig(t *testing.T) {
	covABaseEnv(t)
	logs := covACaptureLogs(t)
	s := covAInitServer(t, covAConfig(t))
	if s.Embedding != nil {
		t.Fatal("no provider is configured yet")
	}
	if err := s.SaveEmbeddingConfig(&EmbeddingConfig{
		ID: "local", Name: "local", Provider: "ollama", Model: "nomic-embed-text",
		Dimensions: 768, APIURL: "http://127.0.0.1:11434", IsDefault: true,
	}); err != nil {
		t.Fatal(err)
	}
	s.initEmbedding()
	if s.Embedding == nil || s.Embedding.Model() != "nomic-embed-text" {
		t.Fatalf("stored config not used, provider = %v", s.Embedding)
	}
	if !strings.Contains(logs.String(), "source=\"stored config\"") {
		t.Fatalf("source not logged:\n%s", logs.String())
	}
}

func TestCovAInitEmbeddingUnconfigured(t *testing.T) {
	covABaseEnv(t)
	logs := covACaptureLogs(t)
	s := covAInitServer(t, covAConfig(t))
	if s.Embedding != nil || s.EmbeddingWorker != nil {
		t.Fatal("nothing configured: no provider, no worker")
	}
	if !strings.Contains(logs.String(), "embedding provider not configured") {
		t.Fatalf("missing provider not reported:\n%s", logs.String())
	}
}

func TestCovAInitAuditExporterErrors(t *testing.T) {
	logs := covACaptureLogs(t)
	db, err := bolt.Open(filepath.Join(t.TempDir(), "a.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	t.Setenv("MDDB_AUDIT_ENABLED", "true")
	t.Setenv("MDDB_AUDIT_RETENTION_DAYS", "not a number")
	t.Setenv("MDDB_AUDIT_EXPORT_BUFFER", "-1")
	t.Setenv("MDDB_AUDIT_EXPORT_WEBHOOK_URL", "   ")
	t.Setenv("MDDB_AUDIT_EXPORT_SYSLOG_ADDR", "   ")
	s := &Server{DB: db}
	covATrapFatal(t)
	s.initAudit()
	defer s.AuditManager.Stop()

	out := logs.String()
	for _, want := range []string{"auditRetention=90", "audit webhook exporter", "audit syslog exporter"} {
		if !strings.Contains(out, want) {
			t.Errorf("log is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "exporter active") {
		t.Error("a blank exporter address must not register an exporter")
	}
}

func TestCovALoadGeoIndexesOnClosedDatabase(t *testing.T) {
	logs := covACaptureLogs(t)
	db, err := bolt.Open(filepath.Join(t.TempDir(), "g.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s := &Server{GeoStore: geo.NewGeoStore(db), GeoIndex: geo.NewGeoIndex(), GeoHashIndex: geo.NewGeoHashIndex()}
	s.loadGeoIndexes()
	if !s.GeoIndex.IsReady() || !s.GeoHashIndex.IsReady() {
		t.Fatal("a failed rebuild must still mark the indexes ready, or geo search stays 503 forever")
	}
	for _, want := range []string{"Geo index rebuild failed", "Geohash index rebuild failed"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log is missing %q", want)
		}
	}
}

func TestCovAWrapHTTPHandler(t *testing.T) {
	covABaseEnv(t)
	logs := covACaptureLogs(t)
	s := covAInitServer(t, covAConfig(t))
	s.Ready = true
	mux := http.NewServeMux()
	s.registerRoutes(mux, false)
	s.registerGraphQL(mux, false)

	h, readTimeout, writeTimeout := s.wrapHTTPHandler(mux, false, "internal")
	if readTimeout <= 0 || writeTimeout <= 0 {
		t.Fatalf("timeouts %v/%v must be positive by default", readTimeout, writeTimeout)
	}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://example.org")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("/health = %d, CORS %q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(logs.String(), "any website can read this instance") {
		t.Fatal("wildcard CORS without auth must be warned about")
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playground", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/playground = %d, want 200", rec.Code)
	}

	ext, _, _ := s.wrapHTTPHandler(mux, false, "external")
	rec = httptest.NewRecorder()
	ext.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("external panel mode must not send CORS headers")
	}
}

func TestCovAOptimizedBoltOptions(t *testing.T) {
	o := getOptimizedBoltOptions()
	if o.Timeout != 2*time.Second || o.FreelistType != bolt.FreelistMapType || o.InitialMmapSize < 100*1024*1024 {
		t.Fatalf("options = %+v", o)
	}
}

// Quantization follows the collection's config; a collection without one,
// or a server without a collection manager, is not quantized.
func TestCovAQuantizationFollowsCollectionConfig(t *testing.T) {
	covABaseEnv(t)
	s := covAInitServer(t, covAConfig(t))
	if err := s.CollectionManager.Set("q", &CollectionConfig{Quantization: "int8"}); err != nil {
		t.Fatal(err)
	}
	vec4 := []float32{0.1, 0.2, 0.3, 0.4}
	for _, c := range []string{"q", "plain"} {
		if err := s.QuantizedVecIndex.Add(c, "d", vec4); err != nil {
			t.Fatal(err)
		}
	}
	if !s.QuantizedVecIndex.HasCollection("q") || s.QuantizedVecIndex.HasCollection("plain") {
		t.Fatal("only the int8 collection should be in the quantized index")
	}
	cm := s.CollectionManager
	s.CollectionManager = nil
	defer func() { s.CollectionManager = cm }()
	if err := s.QuantizedVecIndex.Add("nomanager", "d", vec4); err != nil {
		t.Fatal(err)
	}
	if s.QuantizedVecIndex.HasCollection("nomanager") {
		t.Fatal("without a collection manager nothing is quantized")
	}
}

// Gain counters land in the server's current database.
func TestCovAGainRecorderUsesServerDatabase(t *testing.T) {
	covABaseEnv(t)
	s := covAInitServer(t, covAConfig(t))
	s.Gain.Search(gainKind{Type: "fts"})
	if err := s.Gain.Flush(); err != nil {
		t.Fatal(err)
	}
	found := false
	_ = s.DB.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(bucketStats); b != nil {
			k, _ := b.Cursor().Seek([]byte("gain|"))
			found = k != nil && strings.HasPrefix(string(k), "gain|")
		}
		return nil
	})
	if !found {
		t.Fatal("flushed gain counters are not in the server's database")
	}
}

// Vectors produced by another model than the configured one are reported at
// startup (RAG-007).
func TestCovAStartupReportsStaleEmbeddings(t *testing.T) {
	covABaseEnv(t)
	cfg := covAConfig(t)
	db, err := bolt.Open(cfg.Database.Path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := vector.NewVectorStore(db)
	if err := store.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := store.PutChunks("old", "d", []vector.ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{1, 0}, ChunkHash: "h"}}, "old-model", "h"); err != nil {
		t.Fatal(err)
	}
	if err := store.NoteProvenance("old", "old-model", ""); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	logs := covACaptureLogs(t)
	t.Setenv("MDDB_EMBEDDING_PROVIDER", "ollama")
	t.Setenv("MDDB_EMBEDDING_API_URL", "http://127.0.0.1:9")
	covAInitServer(t, cfg)
	if !strings.Contains(logs.String(), "embedding provenance changed") || !strings.Contains(logs.String(), "old") {
		t.Fatalf("stale collection not reported:\n%s", logs.String())
	}
}
