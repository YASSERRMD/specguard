package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/YASSERRMD/specguard/internal/server"
	"github.com/YASSERRMD/specguard/internal/store"
)

// TestCLIHelperProcess is the helper process called by execCLI to run main() in tests.
func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("BE_CLI_RUN") != "1" {
		return
	}
	main()
}

func execCLI(t *testing.T, serverURL string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"-test.run=TestCLIHelperProcess", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(),
		"BE_CLI_RUN=1",
		"SPECGUARD_SERVER_URL="+serverURL,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestCLI_Subcommands(t *testing.T) {
	// Create mock server to intercept client HTTP requests
	mux := http.NewServeMux()
	mux.HandleFunc("/api/specs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"petstore","status":"saved"}`))
		} else if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`["petstore"]`))
		}
	})
	mux.HandleFunc("/api/mocks/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"protocol-specific mock engine not implemented"}`))
	})
	mux.HandleFunc("/api/mocks/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"protocol-specific mock engine not implemented"}`))
	})
	mux.HandleFunc("/api/contract/run", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"protocol-specific contract runner not implemented"}`))
	})
	mux.HandleFunc("/api/reports/run-123", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"findings":[]}`))
	})
	mux.HandleFunc("/api/reports/run-999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Create a temp file to upload
	tmpFile, err := os.CreateTemp("", "spec-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	_, _ = tmpFile.Write([]byte(`{}`))
	tmpFile.Close()

	// 1. Test version command
	out, err := execCLI(t, srv.URL, "version")
	if err != nil {
		t.Errorf("version command failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "Specguard version") {
		t.Errorf("expected version output, got: %s", out)
	}

	// 2. Test spec add command
	out, err = execCLI(t, srv.URL, "spec", "add", "petstore", tmpFile.Name())
	if err != nil {
		t.Errorf("spec add command failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "added successfully") {
		t.Errorf("expected success message, got: %s", out)
	}

	// 3. Test spec list command
	out, err = execCLI(t, srv.URL, "spec", "list")
	if err != nil {
		t.Errorf("spec list command failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "- petstore") {
		t.Errorf("expected petstore in list, got: %s", out)
	}

	// 4. Test mock start command
	out, err = execCLI(t, srv.URL, "mock", "start", "petstore")
	if err != nil {
		t.Errorf("mock start command failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "Not Implemented") {
		t.Errorf("expected Not Implemented for mock start, got: %s", out)
	}

	// 5. Test mock stop command
	out, err = execCLI(t, srv.URL, "mock", "stop", "petstore")
	if err != nil {
		t.Errorf("mock stop command failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "Not Implemented") {
		t.Errorf("expected Not Implemented for mock stop, got: %s", out)
	}

	// 6. Test contract run command
	out, err = execCLI(t, srv.URL, "contract", "run", "petstore", "http://sut")
	if err != nil {
		t.Errorf("contract run command failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "Not Implemented") {
		t.Errorf("expected Not Implemented for contract run, got: %s", out)
	}

	// 7. Test report show command (found)
	out, err = execCLI(t, srv.URL, "report", "show", "run-123")
	if err != nil {
		t.Errorf("report show command failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "findings") {
		t.Errorf("expected findings in report show output, got: %s", out)
	}

	// 8. Test report show command (not found)
	out, err = execCLI(t, srv.URL, "report", "show", "run-999")
	if err != nil {
		t.Errorf("report show not found command failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "not found") {
		t.Errorf("expected not found message, got: %s", out)
	}
}

func TestCLI_UnknownCommands(t *testing.T) {
	out, err := execCLI(t, "http://localhost", "unknowncommand")
	if err == nil {
		t.Error("expected unknowncommand to fail with non-zero exit code")
	}
	if !strings.Contains(out, "Usage: specguard") {
		t.Errorf("expected usage output, got: %s", out)
	}
}

const testOpenAPISpec = `
openapi: 3.0.0
info:
  title: Test API
  version: 1.0.0
paths:
  /users/{id}:
    get:
      summary: Get User
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
            format: uuid
      responses:
        '200':
          description: Success
`

func TestCLI_IntegrationWithRealServer(t *testing.T) {
	cfg := &server.Config{
		Port:     "0",
		DBDSN:    ":memory:",
		LogLevel: "info",
	}
	dbStore, err := store.NewSQLiteStore(cfg.DBDSN)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer dbStore.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.NewServer(cfg, dbStore, logger)

	testSrv := httptest.NewServer(srv.Handler())
	defer testSrv.Close()

	tmpFile, err := os.CreateTemp("", "openapi-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	_, _ = tmpFile.Write([]byte(testOpenAPISpec))
	tmpFile.Close()

	out, err := execCLI(t, testSrv.URL, "spec", "add", "my-openapi-spec", tmpFile.Name())
	if err != nil {
		t.Errorf("real spec add failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "added successfully") {
		t.Errorf("expected success message, got: %s", out)
	}

	outList, err := execCLI(t, testSrv.URL, "spec", "list")
	if err != nil {
		t.Errorf("real spec list failed: %v, output: %s", err, outList)
	}
	if !strings.Contains(outList, "- my-openapi-spec") {
		t.Errorf("expected my-openapi-spec in list, got: %s", outList)
	}

	// 3. Test mock start command on real server
	outMockStart, err := execCLI(t, testSrv.URL, "mock", "start", "my-openapi-spec")
	if err != nil {
		t.Errorf("real mock start failed: %v, output: %s", err, outMockStart)
	}
	if !strings.Contains(outMockStart, "Status: 200") || !strings.Contains(outMockStart, `"status":"started"`) {
		t.Errorf("expected success start response, got: %s", outMockStart)
	}

	// Extract the address from response to do a quick sanity check
	var startResult struct {
		Address string `json:"address"`
	}
	parts := strings.Split(outMockStart, "Response: ")
	if len(parts) >= 2 {
		jsonStr := parts[1]
		if idx := strings.LastIndex(jsonStr, "}"); idx != -1 {
			jsonStr = jsonStr[:idx+1]
		}
		_ = json.Unmarshal([]byte(jsonStr), &startResult)
	}
	if startResult.Address == "" {
		t.Errorf("mock address not found in output: %s", outMockStart)
	} else {
		// Verify mock server responds to requests
		resp, err := http.Get(startResult.Address + "/users/123e4567-e89b-12d3-a456-426614174000")
		if err != nil {
			t.Errorf("failed to make request to started mock: %v", err)
		} else {
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("expected status 200 from mock server, got: %d", resp.StatusCode)
			}
		}
	}

	// 4. Test mock stop command on real server
	outMockStop, err := execCLI(t, testSrv.URL, "mock", "stop", "my-openapi-spec")
	if err != nil {
		t.Errorf("real mock stop failed: %v, output: %s", err, outMockStop)
	}
	if !strings.Contains(outMockStop, "Status: 200") || !strings.Contains(outMockStop, `"status":"stopped"`) {
		t.Errorf("expected success stop response, got: %s", outMockStop)
	}
}
