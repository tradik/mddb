package main

import (
	"bufio"
	"log"
	"os"
	"time"

	"github.com/tradik/mddb/services/mddb-mcp/internal/config"
	"github.com/tradik/mddb/services/mddb-mcp/internal/mcp"
	"github.com/tradik/mddb/services/mddb-mcp/internal/mddb"
)

func main() {
	// Disable log output to stdout (MCP uses stdout for protocol)
	log.SetOutput(os.Stderr)

	cfgPath := os.Getenv("MDDB_MCP_CONFIG")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Create MDDB client
	client, err := mddb.NewClient(mddb.ClientConfig{
		GRPCAddress:   cfg.MDDB.GRPCAddress,
		RESTBaseURL:   cfg.MDDB.RESTBaseURL,
		TransportMode: cfg.MDDB.TransportMode,
		Timeout:       time.Duration(cfg.MDDB.TimeoutSeconds) * time.Second,
	})
	if err != nil {
		log.Fatalf("failed to create mddb client: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("error closing client: %v", err)
		}
	}()

	// Create MCP handler
	handler := mcp.NewHandler(client)

	// Read from stdin, write to stdout (MCP stdio protocol)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()

		resp, err := handler.HandleJSON(line)
		if err != nil {
			log.Printf("error handling request: %v", err)
			continue
		}

		if _, err := os.Stdout.Write(resp); err != nil {
			log.Printf("error writing response: %v", err)
			continue
		}
		if _, err := os.Stdout.Write([]byte("\n")); err != nil {
			log.Printf("error writing newline: %v", err)
			continue
		}
		if err := os.Stdout.Sync(); err != nil {
			log.Printf("error syncing stdout: %v", err)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("error reading stdin: %v", err)
	}
}
