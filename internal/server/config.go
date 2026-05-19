package server

import (
	"encoding/json"
	"os"
)

// Config holds server configuration settings.
type Config struct {
	Port     string `json:"port"`
	DBDSN    string `json:"db_dsn"`
	LogLevel string `json:"log_level"`
}

// LoadConfig retrieves configurations from environment variables or a file.
func LoadConfig(filePath string) (*Config, error) {
	cfg := &Config{
		Port:     "8080",
		DBDSN:    "specguard.db",
		LogLevel: "info",
	}

	if filePath != "" {
		file, err := os.Open(filePath)
		if err == nil {
			defer file.Close()
			dec := json.NewDecoder(file)
			_ = dec.Decode(cfg)
		}
	}

	if envPort := os.Getenv("SPECGUARD_PORT"); envPort != "" {
		cfg.Port = envPort
	}
	if envDSN := os.Getenv("SPECGUARD_DB_DSN"); envDSN != "" {
		cfg.DBDSN = envDSN
	}
	if envLogLevel := os.Getenv("SPECGUARD_LOG_LEVEL"); envLogLevel != "" {
		cfg.LogLevel = envLogLevel
	}

	return cfg, nil
}
