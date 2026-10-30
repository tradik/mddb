package main

import (
	"mddb/internal/envconf"
	"net/http"
	"os"
	"strings"

	json "mddb/internal/jsonx"
)

// ---- Request/Response types ----

// ConfigResponse represents the server configuration returned by the config endpoint.
type ConfigResponse struct {
	Version               string          `json:"version"`
	DatabasePath          string          `json:"databasePath"`
	Mode                  string          `json:"mode"`
	PanelMode             string          `json:"panelMode"`
	Protocols             ProtocolsConfig `json:"protocols"`
	AuthEnabled           bool            `json:"authEnabled"`
	MetricsEnabled        bool            `json:"metricsEnabled"`
	ReplicationRole       string          `json:"replicationRole"`
	VectorConfig          *VectorConfig   `json:"vectorConfig,omitempty"`
	ChunkConfig           *ChunkConfig    `json:"chunkConfig,omitempty"`
	AutomationsEnabled    bool            `json:"automationsEnabled"`
	AutomationLogsEnabled bool            `json:"automationLogsEnabled"`
	SearchStatsEnabled    bool            `json:"searchStatsEnabled"`
	// ESCompat is the Elasticsearch-compatible search subset (MDDB_ES_COMPAT).
	ESCompat ESCompatStatus `json:"esCompat"`
	// SearchMaxLimit is the most results one search returns
	// (MDDB_SEARCH_MAX_LIMIT, #368); a collection's maxTopK can only lower it.
	SearchMaxLimit int `json:"searchMaxLimit"`
	// Telemetry is OpenTelemetry trace export (OPS-024).
	Telemetry TelemetryStatus `json:"telemetry"`
	// ClientAddressing is how the client address is found behind a proxy (#366).
	ClientAddressing ClientAddressingStatus `json:"clientAddressing"`
}

// ESCompatStatus says whether the Elasticsearch subset is served, and where.
type ESCompatStatus struct {
	Enabled bool   `json:"enabled"`
	Prefix  string `json:"prefix"`
}

// TelemetryStatus says whether traces are exported, and to which collector:
// its scheme and host only (the configuration allowlist's origin class).
type TelemetryStatus struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint,omitempty"`
}

// ClientAddressingStatus is the client-address settings. Trusted proxies are
// counted: their addresses describe the operator's internal network.
type ClientAddressingStatus struct {
	TrustedProxies int    `json:"trustedProxies"`
	Header         string `json:"header"`
	ProxyProtocol  bool   `json:"proxyProtocol"`
}

// ChunkConfig holds the chunking configuration for embeddings.
type ChunkConfig struct {
	Enabled   bool `json:"enabled"`
	ChunkSize int  `json:"chunkSize"`
}

// ProtocolsConfig holds the status of all server protocols.
type ProtocolsConfig struct {
	HTTP  HTTPProtocolStatus  `json:"http"`
	GRPC  GRPCProtocolStatus  `json:"grpc"`
	MCP   MCPProtocolStatus   `json:"mcp"`
	HTTP3 HTTP3ProtocolStatus `json:"http3"`
}

// HTTPProtocolStatus indicates whether the HTTP protocol is enabled and its address.
type HTTPProtocolStatus struct {
	Enabled bool   `json:"enabled"`
	Addr    string `json:"addr"`
}

// GRPCProtocolStatus indicates whether the gRPC protocol is enabled and its address.
type GRPCProtocolStatus struct {
	Enabled bool   `json:"enabled"`
	Addr    string `json:"addr"`
}

// MCPProtocolStatus indicates whether the MCP protocol is enabled and its address.
type MCPProtocolStatus struct {
	Enabled bool   `json:"enabled"`
	Addr    string `json:"addr"`
	Stdio   bool   `json:"stdio"`
	// Revision is the newest MCP spec revision this server serves,
	// SupportedRevisions everything it serves, newest first, and
	// RevisionPinned whether an operator fixed the set to one. Reported here
	// because finding out otherwise means making a request, which answers for
	// one client rather than for the server: it cannot show that a pin is in
	// force, nor what else would have been accepted.
	Revision           string   `json:"revision"`
	SupportedRevisions []string `json:"supportedRevisions"`
	RevisionPinned     bool     `json:"revisionPinned"`
	// HandshakeRevision is what an `initialize` is answered with, or empty
	// when this server serves no handshake revision at all. Since 2026-07-28
	// there is no single revision "in force": a stateless client and a
	// handshake client can be served side by side, and an operator debugging
	// one of them needs to know which of the two they are looking at.
	HandshakeRevision string `json:"handshakeRevision,omitempty"`
}

// HTTP3ProtocolStatus indicates whether the HTTP/3 protocol is enabled and its address.
type HTTP3ProtocolStatus struct {
	Enabled bool   `json:"enabled"`
	Addr    string `json:"addr"`
}

// ---- Handlers ----

// handleConfig returns the current server configuration
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.configResponse(audienceEndpoint)); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}

// configResponse is the effective configuration, every value rendered for
// aud through the configuration allowlist (config_allowlist.go): GET
// /v1/config and the diagnose bundle's config.json are this one answer.
func (s *Server) configResponse(aud configAudience) ConfigResponse {
	response := ConfigResponse{
		Version:      VERSION,
		DatabasePath: configValue("MDDB_PATH", s.Path, aud),
		Mode:         string(s.Mode),
		PanelMode:    env("MDDB_PANEL_MODE", "internal"),
		Protocols: ProtocolsConfig{
			HTTP: HTTPProtocolStatus{
				Enabled: s.Config.HTTP.Enabled,
				Addr:    configValue("MDDB_HTTP_ADDR", s.Config.HTTP.Addr, aud),
			},
			GRPC: GRPCProtocolStatus{
				Enabled: s.Config.GRPC.Enabled,
				Addr:    configValue("MDDB_GRPC_ADDR", s.Config.GRPC.Addr, aud),
			},
			MCP: MCPProtocolStatus{
				Enabled:            s.Config.MCP.Enabled,
				Addr:               configValue("MDDB_MCP_ADDR", s.Config.MCP.Addr, aud),
				Stdio:              s.Config.MCP.Stdio,
				Revision:           activeMCPVersions()[0],
				SupportedRevisions: activeMCPVersions(),
				RevisionPinned:     PinnedMCPVersion() != "",
				HandshakeRevision:  NegotiateMCPVersion(""),
			},
			HTTP3: HTTP3ProtocolStatus{
				Enabled: s.Config.HTTP3.Enabled,
				Addr:    configValue("MDDB_HTTP3_ADDR", s.Config.HTTP3.Addr, aud),
			},
		},
		AuthEnabled:           env("MDDB_AUTH_ENABLED", "false") == "true",
		MetricsEnabled:        env("MDDB_METRICS", "true") != "false",
		ReplicationRole:       s.ReplicationRole,
		AutomationsEnabled:    env("MDDB_AUTOMATIONS", "enable") != "disable",
		AutomationLogsEnabled: env("MDDB_AUTOMATION_LOGS", "enable") != "disable",
		SearchStatsEnabled:    searchStatsEnabled(),
		ESCompat:              ESCompatStatus{Enabled: esCompatEnabled(), Prefix: esPathPrefix},
		SearchMaxLimit:        s.searchMaxLimit(),
		Telemetry:             telemetryStatus(aud),
		ClientAddressing:      clientAddressingStatus(),
	}

	response.VectorConfig = s.vectorConfig()
	if response.VectorConfig != nil {
		response.VectorConfig.APIURL = configValue("MDDB_EMBEDDING_API_URL", response.VectorConfig.APIURL, aud)
	}

	// Add chunk configuration
	chunkEnabled := envconf.String("MDDB_EMBEDDING_CHUNK_ENABLED", "true") == "true"
	chunkSize := envconf.Int("MDDB_EMBEDDING_CHUNK_SIZE", 1500)
	response.ChunkConfig = &ChunkConfig{
		Enabled:   chunkEnabled,
		ChunkSize: chunkSize,
	}
	return response
}

// telemetryStatus is trace export as MDDB_OTEL_ENDPOINT sets it (unset is
// off), the endpoint rendered for aud through the configuration allowlist.
func telemetryStatus(aud configAudience) TelemetryStatus {
	endpoint := strings.TrimSpace(os.Getenv("MDDB_OTEL_ENDPOINT"))
	return TelemetryStatus{Enabled: endpoint != "", Endpoint: configValue("MDDB_OTEL_ENDPOINT", endpoint, aud)}
}

// clientAddressingStatus is the client-address settings installed at startup
// (configureClientAddressing).
func clientAddressingStatus() ClientAddressingStatus {
	st := ClientAddressingStatus{Header: currentClientIPHeader().name, ProxyProtocol: proxyProtocolOn.Load()}
	if list := trustedProxies.Load(); list != nil {
		st.TrustedProxies = len(*list)
	}
	return st
}
