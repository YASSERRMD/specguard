package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/specguard/internal/server"
	"github.com/YASSERRMD/specguard/internal/store"
)

const e2eTestOpenAPISpec = `
openapi: 3.0.0
info:
  title: Test E2E API
  version: 1.0.0
paths:
  /users/{id}:
    get:
      summary: Get user details
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
            format: uuid
        - name: version
          in: query
          required: true
          schema:
            type: integer
      responses:
        '200':
          description: Success
          content:
            application/json:
              schema:
                type: object
                required:
                  - id
                  - name
                properties:
                  id:
                    type: string
                    format: uuid
                  name:
                    type: string
`

func TestRESTPath_EndToEnd(t *testing.T) {
	// 1. Create a temporary directory and SQLite database file
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "specguard_e2e.db")

	cfg := &server.Config{
		Port:     "0", // random port
		DBDSN:    dbPath,
		LogLevel: "info",
	}

	dbStore, err := store.NewSQLiteStore(cfg.DBDSN)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer dbStore.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.NewServer(cfg, dbStore, logger)

	apiSrv := httptest.NewServer(srv.Handler())
	defer apiSrv.Close()
	t.Cleanup(func() {
		_ = srv.Stop(context.Background())
	})

	// Wait for server to be healthy
	healthy := false
	for i := 0; i < 10; i++ {
		resp, err := http.Get(apiSrv.URL + "/health")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			healthy = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthy {
		t.Fatal("Specguard API server failed to become healthy")
	}

	// 2. Write OpenAPI spec to temporary file
	specFile := filepath.Join(tempDir, "e2e_spec.yaml")
	err = os.WriteFile(specFile, []byte(e2eTestOpenAPISpec), 0644)
	if err != nil {
		t.Fatalf("failed to write spec file: %v", err)
	}

	// 3. Register spec via CLI
	out, err := execCLI(t, apiSrv.URL, "spec", "add", "e2e-spec", specFile)
	if err != nil {
		t.Fatalf("spec add failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "added successfully") {
		t.Errorf("expected spec add success, got: %s", out)
	}

	// 4. List specs via CLI
	out, err = execCLI(t, apiSrv.URL, "spec", "list")
	if err != nil {
		t.Fatalf("spec list failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "- e2e-spec") {
		t.Errorf("expected 'e2e-spec' in spec list output, got: %s", out)
	}

	// 5. Start mock server via CLI
	out, err = execCLI(t, apiSrv.URL, "mock", "start", "e2e-spec")
	if err != nil {
		t.Fatalf("mock start failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, `"status":"started"`) {
		t.Errorf("expected status 'started' in mock start output, got: %s", out)
	}

	// Extract mock server address
	var startResult struct {
		Address string `json:"address"`
	}
	parts := strings.Split(out, "Response: ")
	if len(parts) >= 2 {
		jsonStr := parts[1]
		if idx := strings.LastIndex(jsonStr, "}"); idx != -1 {
			jsonStr = jsonStr[:idx+1]
		}
		_ = json.Unmarshal([]byte(jsonStr), &startResult)
	}
	if startResult.Address == "" {
		t.Fatalf("failed to extract mock address from output: %s", out)
	}

	// 6. Verify mock server responds to conforming request
	resp, err := http.Get(startResult.Address + "/users/123e4567-e89b-12d3-a456-426614174000?version=1")
	if err != nil {
		t.Fatalf("failed to send GET request to mock server: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected mock server status 200, got: %d", resp.StatusCode)
	}
	var mockData map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&mockData)
	if err != nil {
		t.Errorf("failed to decode mock response body: %v", err)
	}
	if _, hasID := mockData["id"]; !hasID {
		t.Errorf("expected dynamic field 'id' in mock response, got: %v", mockData)
	}
	if _, hasName := mockData["name"]; !hasName {
		t.Errorf("expected dynamic field 'name' in mock response, got: %v", mockData)
	}

	// 7. Verify scenario selection
	// Query param scenario: not-found -> 404
	respNF, err := http.Get(startResult.Address + "/users/123e4567-e89b-12d3-a456-426614174000?version=1&_scenario=not-found")
	if err != nil {
		t.Fatalf("scenario request failed: %v", err)
	}
	respNF.Body.Close()
	if respNF.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for scenario not-found, got: %d", respNF.StatusCode)
	}

	// Header scenario: server-error -> 500
	reqSE, err := http.NewRequest("GET", startResult.Address+"/users/123e4567-e89b-12d3-a456-426614174000?version=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqSE.Header.Set("X-Mock-Scenario", "server-error")
	respSE, err := http.DefaultClient.Do(reqSE)
	if err != nil {
		t.Fatalf("scenario request failed: %v", err)
	}
	respSE.Body.Close()
	if respSE.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for scenario server-error, got: %d", respSE.StatusCode)
	}

	// 8. Verify chaos injection
	// Latency chaos (verify request latency)
	start := time.Now()
	reqLat, err := http.NewRequest("GET", startResult.Address+"/users/123e4567-e89b-12d3-a456-426614174000?version=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqLat.Header.Set("X-Chaos-Latency", "50ms")
	respLat, err := http.DefaultClient.Do(reqLat)
	if err != nil {
		t.Fatalf("chaos request failed: %v", err)
	}
	respLat.Body.Close()
	duration := time.Since(start)
	if duration < 50*time.Millisecond {
		t.Errorf("expected latency chaos to delay request by at least 50ms, took: %v", duration)
	}

	// Error injection chaos (verify custom status code returned)
	reqErr, err := http.NewRequest("GET", startResult.Address+"/users/123e4567-e89b-12d3-a456-426614174000?version=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqErr.Header.Set("X-Chaos-Error-Rate", "1.0")
	reqErr.Header.Set("X-Chaos-Error-Status", "418")
	respErr, err := http.DefaultClient.Do(reqErr)
	if err != nil {
		t.Fatalf("chaos request failed: %v", err)
	}
	respErr.Body.Close()
	if respErr.StatusCode != http.StatusTeapot {
		t.Errorf("expected chaos error injection status 418, got: %d", respErr.StatusCode)
	}

	// 9. Run contract checks against conforming mock SUT via CLI
	outCheck, err := execCLI(t, apiSrv.URL, "contract", "run", "e2e-spec", startResult.Address)
	if err != nil {
		t.Fatalf("contract run against conforming mock SUT failed: %v, output: %s", err, outCheck)
	}
	if !strings.Contains(outCheck, `"passed":true`) {
		t.Errorf("expected contract checks to pass, got output: %s", outCheck)
	}

	// Extract run_id from output
	runID := extractRunID(outCheck)
	if runID == "" {
		t.Errorf("failed to extract run_id from conforming contract run output: %s", outCheck)
	} else {
		// Verify report show returns no findings
		outReport, err := execCLI(t, apiSrv.URL, "report", "show", runID)
		if err != nil {
			t.Fatalf("report show failed: %v, output: %s", err, outReport)
		}
		if !strings.Contains(outReport, `"findings":[]`) {
			t.Errorf("expected empty findings in conforming run report, got: %s", outReport)
		}
	}

	// 10. Run contract checks against drifting SUT
	driftSrvMux := http.NewServeMux()
	driftSrvMux.HandleFunc("/users/123e4567-e89b-12d3-a456-426614174000", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// violating contract by missing required "name" field
		_, _ = w.Write([]byte(`{"id":"123e4567-e89b-12d3-a456-426614174000"}`))
	})
	driftSrv := httptest.NewServer(driftSrvMux)
	defer driftSrv.Close()

	outDrift, err := execCLI(t, apiSrv.URL, "contract", "run", "e2e-spec", driftSrv.URL)
	if err != nil {
		t.Fatalf("contract run against drifting SUT failed: %v, output: %s", err, outDrift)
	}
	if !strings.Contains(outDrift, `"passed":false`) {
		t.Errorf("expected contract checks to fail for drifting SUT, got output: %s", outDrift)
	}

	// Extract run_id for drifting run
	driftRunID := extractRunID(outDrift)
	if driftRunID == "" {
		t.Errorf("failed to extract run_id from drifting contract run output: %s", outDrift)
	} else {
		// Verify report show returns specific findings
		outReport, err := execCLI(t, apiSrv.URL, "report", "show", driftRunID)
		if err != nil {
			t.Fatalf("report show failed for drifting run: %v, output: %s", err, outReport)
		}
		if !strings.Contains(outReport, "name") || !strings.Contains(outReport, "findings") {
			t.Errorf("expected drift report to highlight missing field 'name', got: %s", outReport)
		}
	}

	// 11. Stop mock server via CLI
	outStop, err := execCLI(t, apiSrv.URL, "mock", "stop", "e2e-spec")
	if err != nil {
		t.Fatalf("mock stop failed: %v, output: %s", err, outStop)
	}
	if !strings.Contains(outStop, `"status":"stopped"`) {
		t.Errorf("expected mock stop success response, got: %s", outStop)
	}

	// Verify mock server has stopped and connection fails
	_, err = http.Get(startResult.Address + "/users/123e4567-e89b-12d3-a456-426614174000?version=1")
	if err == nil {
		t.Errorf("expected mock server GET request to fail after mock stop, but succeeded")
	}
}

func extractRunID(cliOutput string) string {
	var res struct {
		RunID string `json:"run_id"`
	}
	parts := strings.Split(cliOutput, "Response: ")
	if len(parts) >= 2 {
		jsonStr := parts[1]
		if idx := strings.LastIndex(jsonStr, "}"); idx != -1 {
			jsonStr = jsonStr[:idx+1]
		}
		_ = json.Unmarshal([]byte(jsonStr), &res)
	}
	return res.RunID
}
