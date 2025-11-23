package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tradik/mddb/services/mddb-mcp/internal/config"
	"github.com/tradik/mddb/services/mddb-mcp/internal/mcp"
	"github.com/tradik/mddb/services/mddb-mcp/internal/mddb"
)

func main() {
	cfgPath := os.Getenv("MDDB_MCP_CONFIG")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("starting mddb-mcp on %s (mode=%s)", cfg.MCP.ListenAddress, cfg.MDDB.TransportMode)

	// Inicjalizacja klienta MDDB
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

	// Inicjalizacja MCP servera
	server := mcp.NewServer(client, cfg.MCP.ListenAddress)
	if err := server.Start(); err != nil {
		log.Fatalf("failed to start mcp server: %v", err)
	}

	log.Printf("mddb-mcp server running on %s", cfg.MCP.ListenAddress)

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("shutting down mddb-mcp...")
	if err := server.Stop(); err != nil {
		log.Printf("error stopping server: %v", err)
	}
}
