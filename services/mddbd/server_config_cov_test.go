package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Every YAML field, every environment variable and every offline-tool flag
// reaches the configuration (OPS-022 made the flag parsing testable).

const covCfgYAML = `
database: {path: /y/db, mode: read}
http: {enabled: false, addr: ":1"}
grpc: {enabled: false, addr: ":2"}
mcp:
  enabled: true
  addr: ":3"
  stdio: true
  serverInfo: {name: n, description: d, vendor: v, homepage: h}
  instructions: i
http3: {enabled: true, addr: ":4"}
tls: {enabled: true, certFile: c, keyFile: k, clientCAFile: ca, clientAuth: require}
fts: {stemmingEnabled: false, synonymsEnabled: false, defaultLang: pl}
compression: {enabled: false, smallThreshold: 7, mediumThreshold: 9}
vector: {defaultAlgorithm: hnsw, bqRerankFactor: 3}
`

func covCfgParse(t *testing.T, args ...string) (ServerConfig, offlineTool) {
	t.Helper()
	return parseServerConfig(flag.NewFlagSet("mddbd", flag.ContinueOnError), args)
}

func TestCovCfgFileEveryField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mddb.yaml")
	if err := os.WriteFile(path, []byte(covCfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, tool := covCfgParse(t, "-config", path)
	if tool != nil {
		t.Fatal("no tool")
	}
	m := cfg.MCP
	if cfg.Database.Path != "/y/db" || cfg.Database.Mode != ModeRead || cfg.HTTP.Enabled || cfg.HTTP.Addr != ":1" ||
		cfg.GRPC.Enabled || cfg.GRPC.Addr != ":2" || !m.Enabled || m.Addr != ":3" || !m.Stdio ||
		m.ServerInfo.Name != "n" || m.ServerInfo.Description != "d" || m.ServerInfo.Vendor != "v" || m.ServerInfo.Homepage != "h" ||
		m.Instructions != "i" || !cfg.HTTP3.Enabled || cfg.HTTP3.Addr != ":4" || !cfg.TLS.Enabled || cfg.TLS.CertFile != "c" ||
		cfg.TLS.KeyFile != "k" || cfg.TLS.ClientCAFile != "ca" || cfg.TLS.ClientAuth != "require" || cfg.FTS.StemmingEnabled ||
		cfg.FTS.SynonymsEnabled || cfg.FTS.DefaultLang != "pl" || cfg.Compression.Enabled || cfg.Compression.SmallThreshold != 7 ||
		cfg.Compression.MediumThreshold != 9 || cfg.Vector.DefaultAlgorithm != "hnsw" || cfg.Vector.BQRerankFactor != 3 {
		t.Fatalf("file config not applied: %+v", cfg)
	}
	// MDDB_CONFIG names the file when -config does not; a missing file is
	// the defaults, a broken one a warning and the defaults.
	t.Setenv("MDDB_CONFIG", path)
	if cfg, _ = covCfgParse(t); cfg.Vector.DefaultAlgorithm != "hnsw" {
		t.Fatal("MDDB_CONFIG")
	}
	if cfg, _ = covCfgParse(t, "-c", filepath.Join(t.TempDir(), "missing.yaml")); cfg.Vector.DefaultAlgorithm != "flat" {
		t.Fatal("missing file is the defaults")
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("http: [\n"), 0o600)
	if cfg, _ = covCfgParse(t, "-c", bad); cfg.HTTP.Addr != defaultServerConfig().HTTP.Addr {
		t.Fatal("broken file is ignored")
	}
	if _, err := loadConfigFile(t.TempDir()); err == nil {
		t.Fatal("a directory is not a config file")
	}
	if got := mergeFileConfig(defaultServerConfig(), nil); got.HTTP.Addr != defaultServerConfig().HTTP.Addr {
		t.Fatal("nil file config")
	}
}

func TestCovCfgEnvEveryVariable(t *testing.T) {
	for k, v := range map[string]string{
		"MDDB_PATH": "/e/db", "MDDB_MODE": "write", "MDDB_HTTP_ENABLED": "false", "MDDB_ADDR": ":10", "MDDB_API_MODE": "read",
		"MDDB_GRPC_ENABLED": "false", "MDDB_GRPC_ADDR": ":11", "MDDB_GRPC_MODE": "read",
		"MDDB_MCP_ENABLED": "true", "MDDB_MCP_ADDR": ":12", "MDDB_MCP_MODE": "read", "MDDB_MCP_STDIO": "true",
		"MDDB_MCP_DOMAIN": "example.org", "MDDB_MCP_SERVER_NAME": "sn", "MDDB_MCP_SERVER_DESCRIPTION": "sd",
		"MDDB_MCP_SERVER_VENDOR": "sv", "MDDB_MCP_SERVER_HOMEPAGE": "sh",
		"MDDB_EXTREME": "true", "MDDB_HTTP3_ADDR": ":13", "MDDB_HTTP3_MODE": "read",
		"MDDB_TLS_ENABLED": "true", "MDDB_TLS_CERT": "tc", "MDDB_TLS_KEY": "tk", "MDDB_TLS_CLIENT_CA": "tca", "MDDB_TLS_CLIENT_AUTH": "request",
		"MDDB_FTS_STEMMING": "false", "MDDB_FTS_SYNONYMS": "false", "MDDB_FTS_DEFAULT_LANG": "de",
		"MDDB_COMPRESSION_ENABLED": "false", "MDDB_COMPRESSION_SMALL_THRESHOLD": "5", "MDDB_COMPRESSION_MEDIUM_THRESHOLD": "6",
		"MDDB_VECTOR_DEFAULT_ALGORITHM": "ivf", "MDDB_VECTOR_BQ_RERANK_FACTOR": "4",
	} {
		t.Setenv(k, v)
	}
	cfg, _ := covCfgParse(t)
	if cfg.Database.Path != "/e/db" || cfg.Database.Mode != ModeWrite || cfg.HTTP.Enabled || cfg.HTTP.Addr != ":10" ||
		cfg.HTTP.Mode != ModeRead || cfg.GRPC.Enabled || cfg.GRPC.Addr != ":11" || cfg.GRPC.Mode != ModeRead ||
		!cfg.MCP.Enabled || cfg.MCP.Addr != ":12" || cfg.MCP.Mode != ModeRead || !cfg.MCP.Stdio ||
		cfg.MCP.ServerInfo.Name != "sn" || cfg.MCP.ServerInfo.Description != "sd" || cfg.MCP.ServerInfo.Vendor != "sv" ||
		cfg.MCP.ServerInfo.Homepage != "sh" || !cfg.HTTP3.Enabled || cfg.HTTP3.Addr != ":13" || cfg.HTTP3.Mode != ModeRead ||
		!cfg.TLS.Enabled || cfg.TLS.CertFile != "tc" || cfg.TLS.KeyFile != "tk" || cfg.TLS.ClientCAFile != "tca" ||
		cfg.TLS.ClientAuth != "request" || cfg.FTS.StemmingEnabled || cfg.FTS.SynonymsEnabled || cfg.FTS.DefaultLang != "de" ||
		cfg.Compression.Enabled || cfg.Compression.SmallThreshold != 5 || cfg.Compression.MediumThreshold != 6 ||
		cfg.Vector.DefaultAlgorithm != "ivf" || cfg.Vector.BQRerankFactor != 4 {
		t.Fatalf("env not applied: %+v", cfg)
	}
	// The port variables win over the address ones; MDDB_HTTP_ADDR over MDDB_ADDR.
	for k, v := range map[string]string{"MDDB_HTTP_ADDR": ":20", "MDDB_GRPC_PORT": "21", "MDDB_MCP_PORT": "22",
		"MDDB_HTTP3_ENABLED": "false", "MDDB_HTTP3_PORT": "23", "MDDB_HTTP_PORT": "24"} {
		t.Setenv(k, v)
	}
	cfg, _ = covCfgParse(t)
	if cfg.HTTP.Addr != ":24" || cfg.GRPC.Addr != ":21" || cfg.MCP.Addr != ":22" || cfg.HTTP3.Addr != ":23" || cfg.HTTP3.Enabled {
		t.Fatalf("ports: %+v", cfg)
	}
	// Flags beat the environment.
	cfg, _ = covCfgParse(t, "-db", "/f/db", "-http-addr", ":30")
	if cfg.Database.Path != "/f/db" || cfg.HTTP.Addr != ":30" {
		t.Fatalf("flags: %+v", cfg)
	}
	// Numbers that do not parse, or are not positive, keep the defaults.
	t.Setenv("MDDB_COMPRESSION_SMALL_THRESHOLD", "x")
	t.Setenv("MDDB_COMPRESSION_MEDIUM_THRESHOLD", "-1")
	t.Setenv("MDDB_VECTOR_BQ_RERANK_FACTOR", "0")
	cfg, _ = covCfgParse(t)
	d := defaultServerConfig()
	if cfg.Compression.SmallThreshold != d.Compression.SmallThreshold || cfg.Compression.MediumThreshold != d.Compression.MediumThreshold ||
		cfg.Vector.BQRerankFactor != d.Vector.BQRerankFactor {
		t.Fatalf("bad numbers: %+v", cfg)
	}
}

func TestCovCfgOfflineTools(t *testing.T) {
	_, tool := covCfgParse(t, "-version")
	var out bytes.Buffer
	if tool == nil || tool(&out) != 0 || out.String() != "mddbd version "+VERSION+"\n" {
		t.Fatalf("-version: %q", out.String())
	}
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.db")
	// Each tool is chosen by its flag; on a file that does not exist each
	// reports failure rather than starting the server.
	for _, args := range [][]string{
		{"-verify-db", missing},
		{"-repair-db", missing, "-repair-to", filepath.Join(dir, "r.db")},
		{"-compact-db", missing, "-compact-to", filepath.Join(dir, "c.db")},
		{"-split-collections", missing},
		{"-merge-collections", missing},
	} {
		_, tool := covCfgParse(t, args...)
		if tool == nil {
			t.Fatalf("%v: no tool chosen", args)
		}
		if code := tool(io.Discard); code == 0 {
			t.Fatalf("%v: exit 0 on a missing file", args)
		}
	}
	if _, tool := covCfgParse(t, "-check-update"); tool == nil {
		t.Fatal("-check-update is a tool") // not run: it asks GitHub and exits
	}
}

func TestCovCfgLoadServerConfigUsesProcessFlags(t *testing.T) {
	oldArgs, oldFlags := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = oldArgs, oldFlags })
	os.Args = []string{"mddbd", "-db", "/p/db"}
	flag.CommandLine = flag.NewFlagSet("mddbd", flag.ContinueOnError)
	if cfg := loadServerConfig(); cfg.Database.Path != "/p/db" {
		t.Fatalf("process flags: %+v", cfg.Database)
	}
}
