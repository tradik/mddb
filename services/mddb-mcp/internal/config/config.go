package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v3"
)

// Config is the main MCP configuration structure.
type Config struct {
	MCP  MCPConfig  `yaml:"mcp"`
	MDDB MDDBConfig `yaml:"mddb"`
}

type MCPConfig struct {
	ListenAddress string `yaml:"listenAddress"`
}

type MDDBConfig struct {
	GRPCAddress    string `yaml:"grpcAddress"`
	RESTBaseURL    string `yaml:"restBaseUrl"`
	TransportMode  string `yaml:"transportMode"`
	TimeoutSeconds int    `yaml:"timeoutSeconds"`
	MaxRetries     int    `yaml:"maxRetries"`
}

// envConfig maps environment variables.
type envConfig struct {
	MCPListenAddress string `envconfig:"MCP_LISTEN_ADDRESS"`

	MDDBGRPCAddress   string `envconfig:"MDDB_GRPC_ADDRESS"`
	MDDBRESTBaseURL   string `envconfig:"MDDB_REST_BASE_URL"`
	MDDBTransportMode string `envconfig:"MDDB_TRANSPORT_MODE"`
	MDDBTimeoutSec    int    `envconfig:"MDDB_TIMEOUT_SECONDS"`
	MDDBMaxRetries    int    `envconfig:"MDDB_MAX_RETRIES"`
}

// Load loads config in order: defaults -> YAML -> ENV (overrides).
func Load(path string) (*Config, error) {
	cfg := defaultConfig()

	// 1) YAML (optional)
	if err := loadYAML(path, cfg); err != nil {
		return nil, err
	}

	// 2) ENV (overrides YAML values)
	if err := overrideFromEnv(cfg); err != nil {
		return nil, err
	}

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		MCP: MCPConfig{
			ListenAddress: "0.0.0.0:9000",
		},
		MDDB: MDDBConfig{
			GRPCAddress:    "localhost:11024",
			RESTBaseURL:    "http://localhost:11023",
			TransportMode:  "grpc_with_rest_fallback",
			TimeoutSeconds: 2,
			MaxRetries:     1,
		},
	}
}

func loadYAML(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// brak pliku konfiguracyjnego jest akceptowalny
			return nil
		}
		return fmt.Errorf("read config yaml: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("unmarshal config yaml: %w", err)
	}

	return nil
}

func overrideFromEnv(cfg *Config) error {
	var e envConfig
	if err := envconfig.Process("", &e); err != nil {
		return fmt.Errorf("process env: %w", err)
	}

	if e.MCPListenAddress != "" {
		cfg.MCP.ListenAddress = e.MCPListenAddress
	}

	if e.MDDBGRPCAddress != "" {
		cfg.MDDB.GRPCAddress = e.MDDBGRPCAddress
	}
	if e.MDDBRESTBaseURL != "" {
		cfg.MDDB.RESTBaseURL = e.MDDBRESTBaseURL
	}
	if e.MDDBTransportMode != "" {
		cfg.MDDB.TransportMode = e.MDDBTransportMode
	}
	if e.MDDBTimeoutSec != 0 {
		cfg.MDDB.TimeoutSeconds = e.MDDBTimeoutSec
	}
	if e.MDDBMaxRetries != 0 {
		cfg.MDDB.MaxRetries = e.MDDBMaxRetries
	}

	return nil
}

func validate(cfg *Config) error {
	switch cfg.MDDB.TransportMode {
	case "grpc_only", "rest_only", "grpc_with_rest_fallback", "rest_with_grpc_fallback":
		// ok
	default:
		return fmt.Errorf("invalid transportMode: %s", cfg.MDDB.TransportMode)
	}

	if cfg.MDDB.GRPCAddress == "" {
		return errors.New("mddb.grpcAddress is required")
	}
	if cfg.MDDB.RESTBaseURL == "" {
		return errors.New("mddb.restBaseUrl is required")
	}

	return nil
}
