package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// DatabaseConfig controls the BoltDB database file location and access mode.
type DatabaseConfig struct {
	Path string     `yaml:"path" json:"path"` // Database file path (default: "mddb.db")
	Mode AccessMode `yaml:"mode" json:"mode"` // Access mode: "read", "write", or "wr" (default: "wr")
}

// ServerConfig holds all configurable server settings.
// Precedence: CLI flags > env vars > config file > defaults.
type ServerConfig struct {
	Database    DatabaseConfig    `yaml:"database" json:"database"`
	HTTP        HTTPConfig        `yaml:"http" json:"http"`
	GRPC        GRPCConfig        `yaml:"grpc" json:"grpc"`
	MCP         MCPConfig         `yaml:"mcp" json:"mcp"`
	HTTP3       HTTP3Config       `yaml:"http3" json:"http3"`
	TLS         TLSConfig         `yaml:"tls" json:"tls"`
	FTS         FTSConfig         `yaml:"fts" json:"fts"`
	Compression CompressionConfig `yaml:"compression" json:"compression"`
	Vector      VectorExtConfig   `yaml:"vector" json:"vector"`
}

// FTSConfig controls full-text search features.
type FTSConfig struct {
	StemmingEnabled bool   `yaml:"stemmingEnabled" json:"stemmingEnabled"`
	SynonymsEnabled bool   `yaml:"synonymsEnabled" json:"synonymsEnabled"`
	DefaultLang     string `yaml:"defaultLang" json:"defaultLang"`
}

// CompressionConfig controls document compression.
type CompressionConfig struct {
	Enabled         bool `yaml:"enabled" json:"enabled"`
	SmallThreshold  int  `yaml:"smallThreshold" json:"smallThreshold"`
	MediumThreshold int  `yaml:"mediumThreshold" json:"mediumThreshold"`
}

// VectorExtConfig controls extended vector search options.
type VectorExtConfig struct {
	DefaultAlgorithm string `yaml:"defaultAlgorithm" json:"defaultAlgorithm"`
	BQRerankFactor   int    `yaml:"bqRerankFactor" json:"bqRerankFactor"`
}

// TLSConfig controls built-in TLS/HTTPS support.
//
// Set Enabled=true with CertFile+KeyFile to serve HTTPS. To additionally
// require client certificates (mTLS), set ClientCAFile to a PEM bundle of
// trusted CAs; MDDB will then reject any TCP client that does not present a
// certificate chaining to one of those CAs. mTLS is ignored on UDS listeners
// (filesystem permissions already authenticate the peer).
type TLSConfig struct {
	Enabled      bool   `yaml:"enabled" json:"enabled"`
	CertFile     string `yaml:"certFile" json:"certFile"`         // Path to TLS certificate file (PEM)
	KeyFile      string `yaml:"keyFile" json:"keyFile"`           // Path to TLS private key file (PEM)
	ClientCAFile string `yaml:"clientCAFile" json:"clientCAFile"` // Optional: PEM bundle of trusted client CAs (enables mTLS)
	ClientAuth   string `yaml:"clientAuth" json:"clientAuth"`     // Optional: "require" (default when ClientCAFile set) or "request" (verify if presented)
}

// HTTPConfig controls the HTTP/JSON API server.
type HTTPConfig struct {
	Enabled bool       `yaml:"enabled" json:"enabled"`
	Addr    string     `yaml:"addr" json:"addr"`
	Mode    AccessMode `yaml:"mode" json:"mode"` // "read", "write", or "wr" (default: follows MDDB_MODE)
}

// GRPCConfig controls the gRPC server.
type GRPCConfig struct {
	Enabled bool       `yaml:"enabled" json:"enabled"`
	Addr    string     `yaml:"addr" json:"addr"`
	Mode    AccessMode `yaml:"mode" json:"mode"` // "read", "write", or "wr" (default: follows MDDB_MODE)
}

// MCPServerInfo holds customizable server profile returned in MCP initialize response.
type MCPServerInfo struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description,omitempty"`
	Vendor      string `yaml:"vendor" json:"vendor,omitempty"`
	Homepage    string `yaml:"homepage" json:"homepage,omitempty"`
}

// MCPConfig controls the MCP protocol server.
type MCPConfig struct {
	Enabled      bool          `yaml:"enabled" json:"enabled"`
	Addr         string        `yaml:"addr" json:"addr"`
	Stdio        bool          `yaml:"stdio" json:"stdio"`
	Domain       string        `yaml:"domain" json:"domain"`
	Mode         AccessMode    `yaml:"mode" json:"mode"` // "read", "write", or "wr" (default: follows MDDB_MODE)
	ServerInfo   MCPServerInfo `yaml:"serverInfo" json:"serverInfo"`
	Instructions string        `yaml:"instructions" json:"instructions,omitempty"` // System prompt for LLM — how to use this server
}

// HTTP3Config controls the HTTP/3 (QUIC) server (extreme mode).
type HTTP3Config struct {
	Enabled bool       `yaml:"enabled" json:"enabled"`
	Addr    string     `yaml:"addr" json:"addr"`
	Mode    AccessMode `yaml:"mode" json:"mode"` // "read", "write", or "wr" (default: follows MDDB_MODE)
}

// defaultServerConfig returns the default configuration.
func defaultServerConfig() ServerConfig {
	return ServerConfig{
		Database:    DatabaseConfig{Path: "mddb.db", Mode: "wr"},
		HTTP:        HTTPConfig{Enabled: true, Addr: ":11023"},
		GRPC:        GRPCConfig{Enabled: true, Addr: ":11024"},
		MCP:         MCPConfig{Enabled: true, Addr: ":9000", Stdio: false, ServerInfo: MCPServerInfo{Name: "mddbd"}},
		HTTP3:       HTTP3Config{Enabled: false, Addr: ":11443"},
		TLS:         TLSConfig{Enabled: false},
		FTS:         FTSConfig{StemmingEnabled: true, SynonymsEnabled: true, DefaultLang: "en"},
		Compression: CompressionConfig{Enabled: true, SmallThreshold: 1024, MediumThreshold: 10240},
		Vector:      VectorExtConfig{DefaultAlgorithm: "flat", BQRerankFactor: 10},
	}
}

// loadServerConfig loads configuration with precedence: CLI flags > env vars > config file > defaults.
func loadServerConfig() ServerConfig {
	cfg := defaultServerConfig()

	// 1. Parse CLI flags (but don't apply yet — need config file path first)
	var (
		configFile   string
		dbPath       = flag.String("db", "", "Database file path (default: mddb.db)")
		dbMode       = flag.String("mode", "", "Access mode: read, write, or wr (default: wr)")
		httpEnabled  = flag.String("http-enabled", "", "Enable HTTP API server (true/false)")
		httpAddr     = flag.String("http-addr", "", "HTTP API listen address (e.g. :11023)")
		grpcEnabled  = flag.String("grpc-enabled", "", "Enable gRPC server (true/false)")
		grpcAddr     = flag.String("grpc-addr", "", "gRPC listen address (e.g. :11024)")
		mcpEnabled   = flag.String("mcp-enabled", "", "Enable MCP protocol (true/false)")
		mcpAddr      = flag.String("mcp-addr", "", "MCP HTTP listen address (e.g. :9000)")
		mcpStdio     = flag.String("mcp-stdio", "", "Run in MCP stdio mode (true/false)")
		http3Enabled = flag.String("http3-enabled", "", "Enable HTTP/3 server (true/false)")
		http3Addr    = flag.String("http3-addr", "", "HTTP/3 listen address (e.g. :11443)")
		// OPS-019: reports and exits. The daemon never replaces itself — it is
		// a data server, and an unexpected restart is an incident. Installing
		// is `mddb-cli self-update`, run on purpose.
		checkUpdate = flag.Bool("check-update", false, "Report whether a newer release exists, then exit")
	)
	flag.StringVar(&configFile, "config", "", "Path to YAML config file")
	flag.StringVar(&configFile, "c", "", "Path to YAML config file (shorthand)")
	flag.Parse()

	// Before any config is interpreted: this asks GitHub a question and exits,
	// so nothing about this installation needs to be loaded to answer it.
	if *checkUpdate {
		reportUpdateAndExit()
	}

	// 2. Load config file (lowest priority after defaults)
	cfgPath := configFile
	if cfgPath == "" {
		cfgPath = os.Getenv("MDDB_CONFIG")
	}
	if cfgPath != "" {
		fileCfg, err := loadConfigFile(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to load config file %s: %v\n", cfgPath, err)
		} else {
			cfg = mergeFileConfig(cfg, fileCfg)
		}
	}

	// 3. Apply env vars (override config file)
	applyEnvConfig(&cfg)

	// 4. Apply CLI flags (highest priority)
	applyCLIFlags(&cfg, *dbPath, *dbMode, *httpEnabled, *httpAddr, *grpcEnabled, *grpcAddr, *mcpEnabled, *mcpAddr, *mcpStdio, *http3Enabled, *http3Addr)

	return cfg
}

// fileConfig mirrors ServerConfig for YAML unmarshalling with pointer fields
// so we can distinguish "not set" from "set to zero value".
type fileConfig struct {
	Database    *fileDatabase    `yaml:"database"`
	HTTP        *fileHTTP        `yaml:"http"`
	GRPC        *fileGRPC        `yaml:"grpc"`
	MCP         *fileMCP         `yaml:"mcp"`
	HTTP3       *fileHTTP3       `yaml:"http3"`
	TLS         *fileTLS         `yaml:"tls"`
	FTS         *fileFTS         `yaml:"fts"`
	Compression *fileCompression `yaml:"compression"`
	Vector      *fileVector      `yaml:"vector"`
}

type fileDatabase struct {
	Path *string `yaml:"path"`
	Mode *string `yaml:"mode"`
}

type fileTLS struct {
	Enabled      *bool   `yaml:"enabled"`
	CertFile     *string `yaml:"certFile"`
	KeyFile      *string `yaml:"keyFile"`
	ClientCAFile *string `yaml:"clientCAFile"`
	ClientAuth   *string `yaml:"clientAuth"`
}

type fileFTS struct {
	StemmingEnabled *bool   `yaml:"stemmingEnabled"`
	SynonymsEnabled *bool   `yaml:"synonymsEnabled"`
	DefaultLang     *string `yaml:"defaultLang"`
}

type fileCompression struct {
	Enabled         *bool `yaml:"enabled"`
	SmallThreshold  *int  `yaml:"smallThreshold"`
	MediumThreshold *int  `yaml:"mediumThreshold"`
}

type fileVector struct {
	DefaultAlgorithm *string `yaml:"defaultAlgorithm"`
	BQRerankFactor   *int    `yaml:"bqRerankFactor"`
}

type fileHTTP struct {
	Enabled *bool   `yaml:"enabled"`
	Addr    *string `yaml:"addr"`
}

type fileGRPC struct {
	Enabled *bool   `yaml:"enabled"`
	Addr    *string `yaml:"addr"`
}

type fileMCPServerInfo struct {
	Name        *string `yaml:"name"`
	Description *string `yaml:"description"`
	Vendor      *string `yaml:"vendor"`
	Homepage    *string `yaml:"homepage"`
}

type fileMCP struct {
	Enabled      *bool              `yaml:"enabled"`
	Addr         *string            `yaml:"addr"`
	Stdio        *bool              `yaml:"stdio"`
	ServerInfo   *fileMCPServerInfo `yaml:"serverInfo"`
	Instructions *string            `yaml:"instructions"`
}

type fileHTTP3 struct {
	Enabled *bool   `yaml:"enabled"`
	Addr    *string `yaml:"addr"`
}

func loadConfigFile(path string) (*fileConfig, error) {
	// #nosec G304 -- Expected configuration file path
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	return &fc, nil
}

func mergeFileConfig(cfg ServerConfig, fc *fileConfig) ServerConfig {
	if fc == nil {
		return cfg
	}
	if fc.Database != nil {
		if fc.Database.Path != nil {
			cfg.Database.Path = *fc.Database.Path
		}
		if fc.Database.Mode != nil {
			cfg.Database.Mode = AccessMode(*fc.Database.Mode)
		}
	}
	if fc.HTTP != nil {
		if fc.HTTP.Enabled != nil {
			cfg.HTTP.Enabled = *fc.HTTP.Enabled
		}
		if fc.HTTP.Addr != nil {
			cfg.HTTP.Addr = *fc.HTTP.Addr
		}
	}
	if fc.GRPC != nil {
		if fc.GRPC.Enabled != nil {
			cfg.GRPC.Enabled = *fc.GRPC.Enabled
		}
		if fc.GRPC.Addr != nil {
			cfg.GRPC.Addr = *fc.GRPC.Addr
		}
	}
	if fc.MCP != nil {
		if fc.MCP.Enabled != nil {
			cfg.MCP.Enabled = *fc.MCP.Enabled
		}
		if fc.MCP.Addr != nil {
			cfg.MCP.Addr = *fc.MCP.Addr
		}
		if fc.MCP.Stdio != nil {
			cfg.MCP.Stdio = *fc.MCP.Stdio
		}
		if fc.MCP.ServerInfo != nil {
			if fc.MCP.ServerInfo.Name != nil {
				cfg.MCP.ServerInfo.Name = *fc.MCP.ServerInfo.Name
			}
			if fc.MCP.ServerInfo.Description != nil {
				cfg.MCP.ServerInfo.Description = *fc.MCP.ServerInfo.Description
			}
			if fc.MCP.ServerInfo.Vendor != nil {
				cfg.MCP.ServerInfo.Vendor = *fc.MCP.ServerInfo.Vendor
			}
			if fc.MCP.ServerInfo.Homepage != nil {
				cfg.MCP.ServerInfo.Homepage = *fc.MCP.ServerInfo.Homepage
			}
		}
		if fc.MCP.Instructions != nil {
			cfg.MCP.Instructions = *fc.MCP.Instructions
		}
	}
	if fc.HTTP3 != nil {
		if fc.HTTP3.Enabled != nil {
			cfg.HTTP3.Enabled = *fc.HTTP3.Enabled
		}
		if fc.HTTP3.Addr != nil {
			cfg.HTTP3.Addr = *fc.HTTP3.Addr
		}
	}
	if fc.TLS != nil {
		if fc.TLS.Enabled != nil {
			cfg.TLS.Enabled = *fc.TLS.Enabled
		}
		if fc.TLS.CertFile != nil {
			cfg.TLS.CertFile = *fc.TLS.CertFile
		}
		if fc.TLS.KeyFile != nil {
			cfg.TLS.KeyFile = *fc.TLS.KeyFile
		}
		if fc.TLS.ClientCAFile != nil {
			cfg.TLS.ClientCAFile = *fc.TLS.ClientCAFile
		}
		if fc.TLS.ClientAuth != nil {
			cfg.TLS.ClientAuth = *fc.TLS.ClientAuth
		}
	}
	if fc.FTS != nil {
		if fc.FTS.StemmingEnabled != nil {
			cfg.FTS.StemmingEnabled = *fc.FTS.StemmingEnabled
		}
		if fc.FTS.SynonymsEnabled != nil {
			cfg.FTS.SynonymsEnabled = *fc.FTS.SynonymsEnabled
		}
		if fc.FTS.DefaultLang != nil {
			cfg.FTS.DefaultLang = *fc.FTS.DefaultLang
		}
	}
	if fc.Compression != nil {
		if fc.Compression.Enabled != nil {
			cfg.Compression.Enabled = *fc.Compression.Enabled
		}
		if fc.Compression.SmallThreshold != nil {
			cfg.Compression.SmallThreshold = *fc.Compression.SmallThreshold
		}
		if fc.Compression.MediumThreshold != nil {
			cfg.Compression.MediumThreshold = *fc.Compression.MediumThreshold
		}
	}
	if fc.Vector != nil {
		if fc.Vector.DefaultAlgorithm != nil {
			cfg.Vector.DefaultAlgorithm = *fc.Vector.DefaultAlgorithm
		}
		if fc.Vector.BQRerankFactor != nil {
			cfg.Vector.BQRerankFactor = *fc.Vector.BQRerankFactor
		}
	}
	return cfg
}

func applyEnvConfig(cfg *ServerConfig) {
	// Database
	if v := os.Getenv("MDDB_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := os.Getenv("MDDB_MODE"); v != "" {
		cfg.Database.Mode = AccessMode(v)
	}

	// HTTP
	if v := os.Getenv("MDDB_HTTP_ENABLED"); v != "" {
		cfg.HTTP.Enabled = parseBool(v, cfg.HTTP.Enabled)
	}
	// MDDB_ADDR is the legacy env var for HTTP address
	if v := os.Getenv("MDDB_ADDR"); v != "" {
		cfg.HTTP.Addr = v
	}
	if v := os.Getenv("MDDB_HTTP_ADDR"); v != "" {
		cfg.HTTP.Addr = v
	}
	if v := os.Getenv("MDDB_HTTP_PORT"); v != "" {
		cfg.HTTP.Addr = portToAddr(v)
	}
	if v := os.Getenv("MDDB_API_MODE"); v != "" {
		cfg.HTTP.Mode = AccessMode(v)
	}

	// gRPC
	if v := os.Getenv("MDDB_GRPC_ENABLED"); v != "" {
		cfg.GRPC.Enabled = parseBool(v, cfg.GRPC.Enabled)
	}
	if v := os.Getenv("MDDB_GRPC_ADDR"); v != "" {
		cfg.GRPC.Addr = v
	}
	if v := os.Getenv("MDDB_GRPC_PORT"); v != "" {
		cfg.GRPC.Addr = portToAddr(v)
	}
	if v := os.Getenv("MDDB_GRPC_MODE"); v != "" {
		cfg.GRPC.Mode = AccessMode(v)
	}

	// MCP
	if v := os.Getenv("MDDB_MCP_ENABLED"); v != "" {
		cfg.MCP.Enabled = parseBool(v, cfg.MCP.Enabled)
	}
	if v := os.Getenv("MDDB_MCP_ADDR"); v != "" {
		cfg.MCP.Addr = v
	}
	if v := os.Getenv("MDDB_MCP_PORT"); v != "" {
		cfg.MCP.Addr = portToAddr(v)
	}
	if v := os.Getenv("MDDB_MCP_STDIO"); v != "" {
		cfg.MCP.Stdio = parseBool(v, cfg.MCP.Stdio)
	}
	if v := os.Getenv("MDDB_MCP_DOMAIN"); v != "" {
		cfg.MCP.Domain = v
	}
	if v := os.Getenv("MDDB_MCP_SERVER_NAME"); v != "" {
		cfg.MCP.ServerInfo.Name = v
	}
	if v := os.Getenv("MDDB_MCP_SERVER_DESCRIPTION"); v != "" {
		cfg.MCP.ServerInfo.Description = v
	}
	if v := os.Getenv("MDDB_MCP_SERVER_VENDOR"); v != "" {
		cfg.MCP.ServerInfo.Vendor = v
	}
	if v := os.Getenv("MDDB_MCP_SERVER_HOMEPAGE"); v != "" {
		cfg.MCP.ServerInfo.Homepage = v
	}
	if v := os.Getenv("MDDB_MCP_INSTRUCTIONS"); v != "" {
		cfg.MCP.Instructions = v
	}
	if v := os.Getenv("MDDB_MCP_MODE"); v != "" {
		cfg.MCP.Mode = AccessMode(v)
	}

	// TLS
	if v := os.Getenv("MDDB_TLS_ENABLED"); v != "" {
		cfg.TLS.Enabled = parseBool(v, cfg.TLS.Enabled)
	}
	if v := os.Getenv("MDDB_TLS_CERT"); v != "" {
		cfg.TLS.CertFile = v
	}
	if v := os.Getenv("MDDB_TLS_KEY"); v != "" {
		cfg.TLS.KeyFile = v
	}
	if v := os.Getenv("MDDB_TLS_CLIENT_CA"); v != "" {
		cfg.TLS.ClientCAFile = v
	}
	if v := os.Getenv("MDDB_TLS_CLIENT_AUTH"); v != "" {
		cfg.TLS.ClientAuth = v
	}

	// HTTP/3
	// MDDB_EXTREME is the legacy env var — maps to HTTP3 enabled
	if v := os.Getenv("MDDB_EXTREME"); v != "" {
		cfg.HTTP3.Enabled = parseBool(v, cfg.HTTP3.Enabled)
	}
	if v := os.Getenv("MDDB_HTTP3_ENABLED"); v != "" {
		cfg.HTTP3.Enabled = parseBool(v, cfg.HTTP3.Enabled)
	}
	if v := os.Getenv("MDDB_HTTP3_ADDR"); v != "" {
		cfg.HTTP3.Addr = v
	}
	if v := os.Getenv("MDDB_HTTP3_PORT"); v != "" {
		cfg.HTTP3.Addr = portToAddr(v)
	}
	if v := os.Getenv("MDDB_HTTP3_MODE"); v != "" {
		cfg.HTTP3.Mode = AccessMode(v)
	}

	// FTS
	if v := os.Getenv("MDDB_FTS_STEMMING"); v != "" {
		cfg.FTS.StemmingEnabled = parseBool(v, cfg.FTS.StemmingEnabled)
	}
	if v := os.Getenv("MDDB_FTS_SYNONYMS"); v != "" {
		cfg.FTS.SynonymsEnabled = parseBool(v, cfg.FTS.SynonymsEnabled)
	}
	if v := os.Getenv("MDDB_FTS_DEFAULT_LANG"); v != "" {
		cfg.FTS.DefaultLang = v
	}

	// Compression
	if v := os.Getenv("MDDB_COMPRESSION_ENABLED"); v != "" {
		cfg.Compression.Enabled = parseBool(v, cfg.Compression.Enabled)
	}
	if v := os.Getenv("MDDB_COMPRESSION_SMALL_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Compression.SmallThreshold = n
		}
	}
	if v := os.Getenv("MDDB_COMPRESSION_MEDIUM_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Compression.MediumThreshold = n
		}
	}

	// Vector
	if v := os.Getenv("MDDB_VECTOR_DEFAULT_ALGORITHM"); v != "" {
		cfg.Vector.DefaultAlgorithm = v
	}
	if v := os.Getenv("MDDB_VECTOR_BQ_RERANK_FACTOR"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Vector.BQRerankFactor = n
		}
	}
}

func applyCLIFlags(cfg *ServerConfig, dbPath, dbMode, httpEnabled, httpAddr, grpcEnabled, grpcAddr, mcpEnabled, mcpAddr, mcpStdio, http3Enabled, http3Addr string) {
	if dbPath != "" {
		cfg.Database.Path = dbPath
	}
	if dbMode != "" {
		cfg.Database.Mode = AccessMode(dbMode)
	}
	if httpEnabled != "" {
		cfg.HTTP.Enabled = parseBool(httpEnabled, cfg.HTTP.Enabled)
	}
	if httpAddr != "" {
		cfg.HTTP.Addr = httpAddr
	}
	if grpcEnabled != "" {
		cfg.GRPC.Enabled = parseBool(grpcEnabled, cfg.GRPC.Enabled)
	}
	if grpcAddr != "" {
		cfg.GRPC.Addr = grpcAddr
	}
	if mcpEnabled != "" {
		cfg.MCP.Enabled = parseBool(mcpEnabled, cfg.MCP.Enabled)
	}
	if mcpAddr != "" {
		cfg.MCP.Addr = mcpAddr
	}
	if mcpStdio != "" {
		cfg.MCP.Stdio = parseBool(mcpStdio, cfg.MCP.Stdio)
	}
	if http3Enabled != "" {
		cfg.HTTP3.Enabled = parseBool(http3Enabled, cfg.HTTP3.Enabled)
	}
	if http3Addr != "" {
		cfg.HTTP3.Addr = http3Addr
	}
}

func parseBool(s string, fallback bool) bool {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return fallback
	}
	return b
}

// portToAddr converts a plain port number "9000" to ":9000" address format.
func portToAddr(port string) string {
	if port == "" {
		return ""
	}
	// Already has colon prefix
	if port[0] == ':' {
		return port
	}
	return ":" + port
}
