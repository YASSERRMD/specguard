package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
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
