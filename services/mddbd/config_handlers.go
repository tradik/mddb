package main

import (
	"mddb/internal/envconf"
	"net/http"
	"os"

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
}

// HTTP3ProtocolStatus indicates whether the HTTP/3 protocol is enabled and its address.
type HTTP3ProtocolStatus struct {
	Enabled bool   `json:"enabled"`
	Addr    string `json:"addr"`
}

// VectorConfig holds the vector/embedding configuration settings.
type VectorConfig struct {
	Enabled    bool   `json:"enabled"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	APIURL     string `json:"apiUrl"`
}

// ---- Handlers ----

// handleConfig returns the current server configuration
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Build configuration response
	response := ConfigResponse{
		Version:      VERSION,
		DatabasePath: s.Path,
		Mode:         string(s.Mode),
		PanelMode:    env("MDDB_PANEL_MODE", "internal"),
		Protocols: ProtocolsConfig{
			HTTP: HTTPProtocolStatus{
				Enabled: s.Config.HTTP.Enabled,
				Addr:    s.Config.HTTP.Addr,
			},
			GRPC: GRPCProtocolStatus{
				Enabled: s.Config.GRPC.Enabled,
				Addr:    s.Config.GRPC.Addr,
			},
			MCP: MCPProtocolStatus{
				Enabled: s.Config.MCP.Enabled,
				Addr:    s.Config.MCP.Addr,
				Stdio:   s.Config.MCP.Stdio,
			},
			HTTP3: HTTP3ProtocolStatus{
				Enabled: s.Config.HTTP3.Enabled,
				Addr:    s.Config.HTTP3.Addr,
			},
		},
		AuthEnabled:           env("MDDB_AUTH_ENABLED", "false") == "true",
		MetricsEnabled:        env("MDDB_METRICS", "true") != "false",
		ReplicationRole:       s.ReplicationRole,
		AutomationsEnabled:    env("MDDB_AUTOMATIONS", "enable") != "disable",
		AutomationLogsEnabled: env("MDDB_AUTOMATION_LOGS", "enable") != "disable",
		SearchStatsEnabled:    searchStatsEnabled(),
	}

	// Add vector configuration if embedding provider is set
	provider := os.Getenv("MDDB_EMBEDDING_PROVIDER")
	if provider != "" {
		var apiURL, model string
		var dimensions int

		switch provider {
		case "openai":
			apiURL = envconf.String("MDDB_EMBEDDING_API_URL", "https://api.openai.com/v1")
			model = envconf.String("MDDB_EMBEDDING_MODEL", "text-embedding-3-small")
			dimensions = envconf.Int("MDDB_EMBEDDING_DIMENSIONS", 1536)
		case "ollama":
			apiURL = envconf.String("MDDB_EMBEDDING_API_URL", "http://localhost:11434")
			model = envconf.String("MDDB_EMBEDDING_MODEL", "nomic-embed-text")
			dimensions = envconf.Int("MDDB_EMBEDDING_DIMENSIONS", 768)
		case "voyage":
			apiURL = envconf.String("MDDB_EMBEDDING_API_URL", "https://api.voyageai.com/v1")
			model = envconf.String("MDDB_EMBEDDING_MODEL", "voyage-3")
			dimensions = envconf.Int("MDDB_EMBEDDING_DIMENSIONS", 1024)
		}

		response.VectorConfig = &VectorConfig{
			Enabled:    true,
			Provider:   provider,
			Model:      model,
			Dimensions: dimensions,
			APIURL:     apiURL,
		}
	}

	// Add chunk configuration
	chunkEnabled := envconf.String("MDDB_EMBEDDING_CHUNK_ENABLED", "true") == "true"
	chunkSize := envconf.Int("MDDB_EMBEDDING_CHUNK_SIZE", 1500)
	response.ChunkConfig = &ChunkConfig{
		Enabled:   chunkEnabled,
		ChunkSize: chunkSize,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, `{"error":"failed to encode response"}`, http.StatusInternalServerError)
	}
}
