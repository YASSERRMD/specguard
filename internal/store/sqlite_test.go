package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YASSERRMD/specguard/internal/core"
)

func TestSQLiteStore_FreshDatabaseMigration(t *testing.T) {
	// Create a temporary database file in a temporary directory
	tempDir, err := os.MkdirTemp("", "specguard-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.Close()

	// Verify tables are created by running a simple query
	_, err = store.db.Exec("SELECT id FROM specs LIMIT 0")
	if err != nil {
		t.Errorf("specs table was not created: %v", err)
	}

	_, err = store.db.Exec("SELECT id FROM mock_configs LIMIT 0")
	if err != nil {
		t.Errorf("mock_configs table was not created: %v", err)
	}

	_, err = store.db.Exec("SELECT id FROM contract_runs LIMIT 0")
	if err != nil {
		t.Errorf("contract_runs table was not created: %v", err)
	}

	_, err = store.db.Exec("SELECT version FROM schema_migrations LIMIT 0")
	if err != nil {
		t.Errorf("schema_migrations table was not created: %v", err)
	}
}

func TestSQLiteStore_SpecLifecycle(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}
	defer store.Close()

	specID := "user-service"
	spec := &core.NormalizedSpec{
		Operations: map[string]core.Operation{
			"GetUser": {
				ID: "GetUser",
				Input: core.Schema{
					Type: core.TypeObject,
					Properties: map[string]core.Schema{
						"id": {Type: core.TypeScalar, ScalarType: core.ScalarString},
					},
					Required: []string{"id"},
				},
				Output: core.Schema{
					Type: core.TypeObject,
					Properties: map[string]core.Schema{
						"name": {Type: core.TypeScalar, ScalarType: core.ScalarString},
					},
				},
			},
		},
	}

	// Save spec
	if err := store.SaveSpec(specID, spec); err != nil {
		t.Fatalf("failed to save spec: %v", err)
	}

	// Load spec
	loaded, err := store.LoadSpec(specID)
	if err != nil {
		t.Fatalf("failed to load spec: %v", err)
	}

	if _, exists := loaded.Operations["GetUser"]; !exists {
		t.Fatalf("loaded spec missing GetUser operation")
	}

	op := loaded.Operations["GetUser"]
	if op.Input.Type != core.TypeObject {
		t.Errorf("expected object type, got %s", op.Input.Type)
	}

	// Load non-existent spec
	_, err = store.LoadSpec("non-existent")
	if err == nil {
		t.Error("expected error loading non-existent spec")
	}
}

func TestSQLiteStore_MockConfigLifecycle(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}
	defer store.Close()

	mockID := "user-service-mock"
	config := &core.MockConfig{
		Port: 8080,
		Host: "localhost",
		ProtocolConfig: map[string]interface{}{
			"base_path": "/api/v1",
		},
	}

	if err := store.SaveMockConfig(mockID, config); err != nil {
		t.Fatalf("failed to save mock config: %v", err)
	}

	loaded, err := store.LoadMockConfig(mockID)
	if err != nil {
		t.Fatalf("failed to load mock config: %v", err)
	}

	if loaded.Port != 8080 {
		t.Errorf("expected port 8080, got %d", loaded.Port)
	}

	if loaded.Host != "localhost" {
		t.Errorf("expected host localhost, got %s", loaded.Host)
	}

	if loaded.ProtocolConfig["base_path"] != "/api/v1" {
		t.Errorf("expected base_path /api/v1, got %v", loaded.ProtocolConfig["base_path"])
	}

	_, err = store.LoadMockConfig("non-existent")
	if err == nil {
		t.Error("expected error loading non-existent mock config")
	}
}

func TestSQLiteStore_ContractRuns(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}
	defer store.Close()

	specID := "payment-service"
	run1 := &ContractRun{
		ID:        "run-1",
		SpecID:    specID,
		TargetURL: "http://localhost:8081",
		Passed:    true,
		DriftReport: &core.DriftReport{
			Findings: []core.Finding{},
		},
		CreatedAt: time.Now().Add(-10 * time.Minute),
	}

	run2 := &ContractRun{
		ID:        "run-2",
		SpecID:    specID,
		TargetURL: "http://localhost:8081",
		Passed:    false,
		DriftReport: &core.DriftReport{
			Findings: []core.Finding{
				{
					Location: "output.properties.amount",
					Kind:     core.KindTypeChanged,
					Expected: "number",
					Actual:   "string",
					Severity: core.SeverityError,
				},
			},
		},
		CreatedAt: time.Now().Add(-5 * time.Minute),
	}

	// Save runs
	if err := store.SaveContractRun(run1); err != nil {
		t.Fatalf("failed to save run1: %v", err)
	}
	if err := store.SaveContractRun(run2); err != nil {
		t.Fatalf("failed to save run2: %v", err)
	}

	// Get individual run
	loadedRun, err := store.GetContractRun("run-2")
	if err != nil {
		t.Fatalf("failed to get run2: %v", err)
	}
	if loadedRun.Passed {
		t.Error("expected loaded run2 to show failed status")
	}
	if len(loadedRun.DriftReport.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(loadedRun.DriftReport.Findings))
	}
	if loadedRun.DriftReport.Findings[0].Location != "output.properties.amount" {
		t.Errorf("unexpected finding location: %s", loadedRun.DriftReport.Findings[0].Location)
	}

	// List runs for spec ID (should be sorted by date DESC, so run2 comes first)
	runs, err := store.ListContractRuns(specID)
	if err != nil {
		t.Fatalf("failed to list runs: %v", err)
	}

	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}

	if runs[0].ID != "run-2" {
		t.Errorf("expected run-2 to be first (newest), got %s", runs[0].ID)
	}
	if runs[1].ID != "run-1" {
		t.Errorf("expected run-1 to be second, got %s", runs[1].ID)
	}
}
