package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// MCPFileConfig is the YAML file structure for custom tools.
type MCPFileConfig struct {
	CustomTools []MCPCustomToolConfig `yaml:"custom_tools"`
}

// loadMCPCustomTools loads custom tool definitions from YAML file (if configured).
func loadMCPCustomTools() []MCPCustomToolConfig {
	path := os.Getenv("MDDB_MCP_CONFIG")
	if path == "" {
		return nil
	}

	cfg, err := loadMCPConfig(path)
	if err != nil {
		slog.Warn("failed to load MCP config", "path", path, "err", err) // #nosec G706 -- internal log
		return nil
	}

	if err := validateMCPCustomTools(cfg.CustomTools); err != nil {
		slog.Warn("invalid MCP custom tools", "err", err)
		return nil
	}

	if len(cfg.CustomTools) > 0 {
		slog.Info("MCP loaded custom tools", "customToolsCount", len(cfg.CustomTools), "path", path) // #nosec G706 -- internal log
	}

	return cfg.CustomTools
}

func loadMCPConfig(path string) (*MCPFileConfig, error) {
	// #nosec G304 -- Expected configuration dynamically loaded by MCP execution
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &MCPFileConfig{}, nil
		}
		return nil, err
	}

	var cfg MCPFileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
