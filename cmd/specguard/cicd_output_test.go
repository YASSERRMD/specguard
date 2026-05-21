package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YASSERRMD/specguard/internal/core"
)

func TestCICDOutput_ConformingText(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/contract/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"run_id": "run-conforming",
			"status": "completed",
			"passed": true,
			"drift_report": map[string]interface{}{
				"findings": []interface{}{},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := execCLI(t, srv.URL, "contract", "run", "test-spec", "http://localhost:8080")
	if err != nil {
		t.Fatalf("expected command to succeed, got error: %v, output: %s", err, out)
	}

	if !strings.Contains(out, "Status: PASSED") {
		t.Errorf("expected Status: PASSED in output, got: %s", out)
	}
	if !strings.Contains(out, "Run ID: run-conforming") {
		t.Errorf("expected Run ID in output, got: %s", out)
	}
	if !strings.Contains(out, "No drift or validation errors found.") {
		t.Errorf("expected no drift message, got: %s", out)
	}
}

func TestCICDOutput_ConformingJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/contract/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"run_id": "run-conforming-json",
			"status": "completed",
			"passed": true,
			"drift_report": map[string]interface{}{
				"findings": []interface{}{},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := execCLI(t, srv.URL, "contract", "run", "test-spec", "http://localhost:8080", "--format", "json")
	if err != nil {
		t.Fatalf("expected command to succeed, got error: %v, output: %s", err, out)
	}

	var res struct {
		RunID    string         `json:"run_id"`
		Passed   bool           `json:"passed"`
		Findings []core.Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v, raw output: %s", err, out)
	}

	if res.RunID != "run-conforming-json" {
		t.Errorf("expected run_id run-conforming-json, got %s", res.RunID)
	}
	if !res.Passed {
		t.Errorf("expected passed true, got false")
	}
	if len(res.Findings) != 0 {
		t.Errorf("expected empty findings, got %d", len(res.Findings))
	}
}

func TestCICDOutput_ConformingJUnit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/contract/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"run_id": "run-conforming-junit",
			"status": "completed",
			"passed": true,
			"drift_report": map[string]interface{}{
				"findings": []interface{}{},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := execCLI(t, srv.URL, "contract", "run", "test-spec", "http://localhost:8080", "--format", "junit")
	if err != nil {
		t.Fatalf("expected command to succeed, got error: %v, output: %s", err, out)
	}

	if !strings.Contains(out, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("expected XML header, got: %s", out)
	}
	if !strings.Contains(out, `testsuites name="specguard-contract" tests="1" failures="0"`) {
		t.Errorf("expected 0 failures in testsuites, got: %s", out)
	}
	if !strings.Contains(out, `<testcase name="contract-run" className="specguard.test-spec"`) {
		t.Errorf("expected testcase tag, got: %s", out)
	}
}

func TestCICDOutput_DriftText(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/contract/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"run_id": "run-drift-text",
			"status": "completed",
			"passed": false,
			"drift_report": map[string]interface{}{
				"findings": []map[string]interface{}{
					{
						"location": "operations.getUser.output.200",
						"kind":     "constraint-violated",
						"expected": "conformant schema structure",
						"actual":   "missing standard response format",
						"severity": "error",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := execCLI(t, srv.URL, "contract", "run", "test-spec", "http://localhost:8080")
	if err == nil {
		t.Fatalf("expected command to fail (exit 1), but succeeded. Output: %s", out)
	}

	if !strings.Contains(out, "Status: FAILED") {
		t.Errorf("expected Status: FAILED in output, got: %s", out)
	}
	if !strings.Contains(out, "Location: operations.getUser.output.200") {
		t.Errorf("expected Location in findings, got: %s", out)
	}
	if !strings.Contains(out, "Kind:     constraint-violated") {
		t.Errorf("expected Kind in findings, got: %s", out)
	}
}

func TestCICDOutput_DriftJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/contract/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"run_id": "run-drift-json",
			"status": "completed",
			"passed": false,
			"drift_report": map[string]interface{}{
				"findings": []map[string]interface{}{
					{
						"location": "operations.getUser.output.200",
						"kind":     "constraint-violated",
						"expected": "conformant schema structure",
						"actual":   "missing standard response format",
						"severity": "error",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := execCLI(t, srv.URL, "contract", "run", "test-spec", "http://localhost:8080", "--format", "json")
	if err == nil {
		t.Fatalf("expected command to fail (exit 1), but succeeded. Output: %s", out)
	}

	var res struct {
		RunID    string         `json:"run_id"`
		Passed   bool           `json:"passed"`
		Findings []core.Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v, raw output: %s", err, out)
	}

	if res.RunID != "run-drift-json" {
		t.Errorf("expected run_id run-drift-json, got %s", res.RunID)
	}
	if res.Passed {
		t.Errorf("expected passed false, got true")
	}
	if len(res.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(res.Findings))
	}
	if res.Findings[0].Location != "operations.getUser.output.200" {
		t.Errorf("expected location operations.getUser.output.200, got %s", res.Findings[0].Location)
	}
}

func TestCICDOutput_DriftJUnit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/contract/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"run_id": "run-drift-junit",
			"status": "completed",
			"passed": false,
			"drift_report": map[string]interface{}{
				"findings": []map[string]interface{}{
					{
						"location": "operations.getUser.output.200",
						"kind":     "constraint-violated",
						"expected": "conformant schema structure",
						"actual":   "missing standard response format",
						"severity": "error",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := execCLI(t, srv.URL, "contract", "run", "test-spec", "http://localhost:8080", "--format", "junit")
	if err == nil {
		t.Fatalf("expected command to fail (exit 1), but succeeded. Output: %s", out)
	}

	if !strings.Contains(out, `testsuites name="specguard-contract" tests="1" failures="1"`) {
		t.Errorf("expected 1 failure in testsuites, got: %s", out)
	}
	if !strings.Contains(out, `<testcase name="getUser" className="specguard.test-spec"`) {
		t.Errorf("expected testcase with operation name getUser, got: %s", out)
	}
	if !strings.Contains(out, `<failure message="Drift detected: constraint-violated (Expected: conformant schema structure, Actual: missing standard response format)" type="constraint-violated">`) {
		t.Errorf("expected failure message, got: %s", out)
	}
}

func TestCICDOutput_InvalidFormat(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/contract/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"run_id": "run-ok",
			"status": "completed",
			"passed": true,
			"drift_report": map[string]interface{}{
				"findings": []interface{}{},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := execCLI(t, srv.URL, "contract", "run", "test-spec", "http://localhost:8080", "--format", "invalid")
	if err == nil {
		t.Fatalf("expected command to fail (exit 1) on invalid format, but succeeded. Output: %s", out)
	}
	if !strings.Contains(out, `Unknown format "invalid"`) {
		t.Errorf("expected error message for unknown format, got: %s", out)
	}
}

func TestCICDOutput_NotImplemented(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/contract/run", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		resp := map[string]interface{}{
			"error": "protocol-specific contract runner not implemented",
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := execCLI(t, srv.URL, "contract", "run", "test-spec", "http://localhost:8080")
	if err != nil {
		t.Fatalf("expected command to exit 0 for NotImplemented, got error: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "Not Implemented:") {
		t.Errorf("expected NotImplemented output message, got: %s", out)
	}
}

func TestCICDOutput_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/contract/run", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error occurred"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := execCLI(t, srv.URL, "contract", "run", "test-spec", "http://localhost:8080")
	if err == nil {
		t.Fatalf("expected command to exit 1 for server 500 error, but succeeded. Output: %s", out)
	}
	if !strings.Contains(out, "API request failed with status 500") {
		t.Errorf("expected internal error message, got: %s", out)
	}
}
