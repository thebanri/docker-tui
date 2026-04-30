package models

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type SSHHost struct {
	Host       string `json:"host"`
	Port       string `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
}

type Config struct {
	SavedHosts []SSHHost `json:"saved_hosts"`
}

func getConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "docker-tui-config.json"
	}
	return filepath.Join(home, ".docker-tui.json")
}

func LoadConfig() Config {
	var cfg Config
	data, err := os.ReadFile(getConfigPath())
	if err == nil {
		json.Unmarshal(data, &cfg)
	}
	return cfg
}

func SaveConfig(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(getConfigPath(), data, 0600)
}

func AddHostToConfig(host SSHHost) {
	cfg := LoadConfig()
	// Check if already exists, update if it does
	found := false
	for i, h := range cfg.SavedHosts {
		if h.Host == host.Host && h.Port == host.Port && h.Username == host.Username {
			cfg.SavedHosts[i] = host
			found = true
			break
		}
	}
	if !found {
		cfg.SavedHosts = append(cfg.SavedHosts, host)
	}
	SaveConfig(cfg)
}
