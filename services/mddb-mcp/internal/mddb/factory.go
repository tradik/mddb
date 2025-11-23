package mddb

import (
	"fmt"
	"time"
)

// ClientConfig contains MDDB client configuration.
type ClientConfig struct {
	GRPCAddress   string
	RESTBaseURL   string
	TransportMode string
	Timeout       time.Duration
}

// NewClient creates MDDB client based on configuration.
func NewClient(cfg ClientConfig) (Client, error) {
	mode := TransportMode(cfg.TransportMode)

	var grpcClient, restClient Client
	var err error

	// Initialize clients depending on mode
	switch mode {
	case TransportGRPCOnly, TransportGRPCWithRESTFallback:
		grpcClient, err = NewGRPCClient(cfg.GRPCAddress, cfg.Timeout)
		if err != nil {
			if mode == TransportGRPCOnly {
				return nil, fmt.Errorf("create grpc client: %w", err)
			}
			// In fallback mode, continue with REST
			grpcClient = nil
		}
	}

	switch mode {
	case TransportRESTOnly, TransportRESTWithGRPCFallback, TransportGRPCWithRESTFallback:
		restClient = NewRESTClient(cfg.RESTBaseURL, cfg.Timeout)
	}

	// If gRPC failed in fallback mode, use REST only
	if mode == TransportGRPCWithRESTFallback && grpcClient == nil {
		return restClient, nil
	}

	return NewFallbackClient(mode, grpcClient, restClient), nil
}
