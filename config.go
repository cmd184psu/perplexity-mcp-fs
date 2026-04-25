package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Port  int      `json:"port"`
	Roots []string `json:"roots"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mcp-fs-sse.json")
}

func loadConfig() Config {
	cfg := Config{Port: 8765}
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	return cfg
}

func saveConfig(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0644)
}
