package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Errors(t *testing.T) {
	// Test non-existent file
	_, err := LoadConfig("non_existent_file.json")
	if err == nil {
		t.Error("expected error for non-existent config file, got nil")
	}

	// Test invalid JSON syntax
	tempDir := t.TempDir()
	badJSONFile := filepath.Join(tempDir, "bad.json")
	if err := os.WriteFile(badJSONFile, []byte("{invalid-json}"), 0644); err != nil {
		t.Fatalf("failed to write temporary file: %v", err)
	}

	_, err = LoadConfig(badJSONFile)
	if err == nil {
		t.Error("expected error for malformed JSON config, got nil")
	}
}

func TestLoadConfig_Success(t *testing.T) {
	tempDir := t.TempDir()
	goodJSONFile := filepath.Join(tempDir, "good.json")
	configJSON := `{"port":"9090","db_dsn":"temp.db","log_level":"debug","api_key":"secret"}`
	if err := os.WriteFile(goodJSONFile, []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write temporary file: %v", err)
	}

	cfg, err := LoadConfig(goodJSONFile)
	if err != nil {
		t.Fatalf("unexpected error loading valid config: %v", err)
	}

	if cfg.Port != "9090" {
		t.Errorf("expected port 9090, got %s", cfg.Port)
	}
	if cfg.DBDSN != "temp.db" {
		t.Errorf("expected db_dsn temp.db, got %s", cfg.DBDSN)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected log_level debug, got %s", cfg.LogLevel)
	}
	if cfg.APIKey != "secret" {
		t.Errorf("expected api_key secret, got %s", cfg.APIKey)
	}
}
