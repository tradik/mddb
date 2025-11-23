package mddb

import (
	"fmt"
	"time"
)

// ClientConfig zawiera konfigurację klienta MDDB.
type ClientConfig struct {
	GRPCAddress   string
	RESTBaseURL   string
	TransportMode string
	Timeout       time.Duration
}

// NewClient tworzy klienta MDDB na podstawie konfiguracji.
func NewClient(cfg ClientConfig) (Client, error) {
	mode := TransportMode(cfg.TransportMode)

	var grpcClient, restClient Client
	var err error

	// Inicjalizuj klientów w zależności od trybu
	switch mode {
	case TransportGRPCOnly, TransportGRPCWithRESTFallback:
		grpcClient, err = NewGRPCClient(cfg.GRPCAddress, cfg.Timeout)
		if err != nil {
			if mode == TransportGRPCOnly {
				return nil, fmt.Errorf("create grpc client: %w", err)
			}
			// W trybie fallback kontynuujemy z REST
			grpcClient = nil
		}
	}

	switch mode {
	case TransportRESTOnly, TransportRESTWithGRPCFallback, TransportGRPCWithRESTFallback:
		restClient = NewRESTClient(cfg.RESTBaseURL, cfg.Timeout)
	}

	// Jeśli gRPC nie udało się w trybie fallback, użyj tylko REST
	if mode == TransportGRPCWithRESTFallback && grpcClient == nil {
		return restClient, nil
	}

	return NewFallbackClient(mode, grpcClient, restClient), nil
}
