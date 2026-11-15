package main

import (
	"fmt"
	"net"
	"net/http"
)

// advertisedAddr is the host:port a client dials for a listen address: the
// MCP domain (MDDB_MCP_DOMAIN) when set, else the listen host, else
// localhost for an empty or unspecified one (":11024", "0.0.0.0:11024").
// It used to prepend "localhost" to the listen address, so a bound address
// such as "127.0.0.1:18924" came out as "localhost127.0.0.1:18924".
func advertisedAddr(domain, listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen // not host:port: hand it back as configured
	}
	switch {
	case domain != "":
		host = domain
	case host == "" || net.ParseIP(host).IsUnspecified():
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

// ---- Handlers ----

// handleMCPConfig returns the MCP server configuration in YAML format
func (s *Server) handleMCPConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get addresses from environment
	httpAddr := env("MDDB_ADDR", ":11023")
	grpcAddr := env("MDDB_GRPC_ADDR", ":11024")
	domain := env("MDDB_MCP_DOMAIN", "")

	// Generate MCP YAML configuration
	yaml := fmt.Sprintf(`mcp:
  # Set listenAddress to run MCP on a dedicated port.
  # Omit to embed MCP on the HTTP API port (%s).
  # listenAddress: "0.0.0.0:9000"

mddb:
  grpcAddress: "%s"
  restBaseUrl: "http://%s"
  # grpc_only | rest_only | grpc_with_rest_fallback | rest_with_grpc_fallback
  transportMode: "grpc_with_rest_fallback"
  timeoutSeconds: 2
  maxRetries: 1

# Custom tools configuration (optional)
# Uncomment and customize as needed:
#
# custom_tools:
#   - name: "semantic_search"
#     defaults:
#       collection: "docs"
#       topK: 5
#       threshold: 0.7
#       includeContent: true
#
#   - name: "search_documents"
#     defaults:
#       collection: "docs"
#       limit: 10
#       sortBy: "addedAt"
#       sortAsc: false
#
#   - name: "full_text_search"
#     defaults:
#       collection: "docs"
#       limit: 10
`, httpAddr, advertisedAddr(domain, grpcAddr), advertisedAddr(domain, httpAddr))

	w.Header().Set("Content-Type", "text/yaml")
	if _, err := w.Write([]byte(yaml)); err != nil {
		http.Error(w, "failed to write response", http.StatusInternalServerError)
	}
}
