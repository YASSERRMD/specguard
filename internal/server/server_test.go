package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YASSERRMD/specguard/internal/core"
	"github.com/YASSERRMD/specguard/internal/store"
)

func newTestServer(t *testing.T) (*Server, store.Store) {
	t.Helper()
	cfg := &Config{
		Port:     "8080",
		DBDSN:    ":memory:",
		LogLevel: "info",
	}
	dbStore, err := store.NewSQLiteStore(cfg.DBDSN)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}

	// Discard logging to keep test output clean
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(cfg, dbStore, logger)
	return srv, dbStore
}

func TestServer_Health(t *testing.T) {
	srv, dbStore := newTestServer(t)
	defer dbStore.Close()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %s", body["status"])
	}
}

func TestServer_Specs(t *testing.T) {
	srv, dbStore := newTestServer(t)
	defer dbStore.Close()

	// 1. Upload pre-parsed spec
	uploadReq := uploadSpecRequest{
		ID: "petstore",
		Spec: &core.NormalizedSpec{
			Operations: map[string]core.Operation{
				"GetPet": {ID: "GetPet"},
			},
		},
	}
	bodyBytes, _ := json.Marshal(uploadReq)
	req := httptest.NewRequest("POST", "/api/specs", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 Created for pre-parsed spec, got %d", resp.StatusCode)
	}

	// 2. Upload raw spec
	rawOpenAPI := `
openapi: 3.0.0
info:
  title: Test API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: Get Pets
      operationId: getPets
      responses:
        '200':
          description: Success
`
	uploadRawReq := uploadSpecRequest{
		ID:  "petstore-raw",
		Raw: rawOpenAPI,
	}
	rawBodyBytes, _ := json.Marshal(uploadRawReq)
	reqRaw := httptest.NewRequest("POST", "/api/specs", bytes.NewReader(rawBodyBytes))
	wRaw := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(wRaw, reqRaw)

	respRaw := wRaw.Result()
	if respRaw.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 Created for raw spec, got %d", respRaw.StatusCode)
	}

	// 3. List specs
	reqList := httptest.NewRequest("GET", "/api/specs", nil)
	wList := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(wList, reqList)

	respList := wList.Result()
	if respList.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", respList.StatusCode)
	}

	var specs []string
	_ = json.NewDecoder(respList.Body).Decode(&specs)
	if len(specs) != 2 {
		t.Errorf("expected 2 specs in list, got: %v", specs)
	}
}

func TestServer_NotImplementedRoutes(t *testing.T) {
	srv, dbStore := newTestServer(t)
	defer dbStore.Close()

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/contract/run"},
	}

	for _, tc := range routes {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()
			srv.server.Handler.ServeHTTP(w, req)

			resp := w.Result()
			if resp.StatusCode != http.StatusNotImplemented {
				t.Errorf("expected 501 Not Implemented, got %d", resp.StatusCode)
			}

			var body map[string]string
			_ = json.NewDecoder(resp.Body).Decode(&body)
			if !strings.Contains(body["error"], "not implemented") {
				t.Errorf("expected error message containing 'not implemented', got %s", body["error"])
			}
		})
	}
}

func TestServer_Reports(t *testing.T) {
	srv, dbStore := newTestServer(t)
	defer dbStore.Close()

	// 1. Populate a contract run in database
	run := &store.ContractRun{
		ID:        "run-123",
		SpecID:    "petstore",
		TargetURL: "http://example.com",
		Passed:    false,
		DriftReport: &core.DriftReport{
			Findings: []core.Finding{
				{
					Location: "input.properties.id",
					Kind:     core.KindMissing,
					Expected: "present",
					Actual:   "missing",
					Severity: core.SeverityError,
				},
			},
		},
	}
	if err := dbStore.SaveContractRun(run); err != nil {
		t.Fatalf("failed to seed run: %v", err)
	}

	// 2. Query reports endpoint
	req := httptest.NewRequest("GET", "/api/reports/run-123", nil)
	w := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	var report core.DriftReport
	_ = json.NewDecoder(resp.Body).Decode(&report)
	if len(report.Findings) != 1 || report.Findings[0].Location != "input.properties.id" {
		t.Errorf("unexpected drift report findings: %v", report.Findings)
	}

	// 3. Query non-existent report
	reqNonExistent := httptest.NewRequest("GET", "/api/reports/run-999", nil)
	wNonExistent := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(wNonExistent, reqNonExistent)

	respNonExistent := wNonExistent.Result()
	if respNonExistent.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", respNonExistent.StatusCode)
	}
}

func TestServer_MockManagement(t *testing.T) {
	srv, dbStore := newTestServer(t)
	defer dbStore.Close()

	// 1. Upload a spec
	rawOpenAPI := `
openapi: 3.0.3
info:
  title: Test Spec
  version: 1.0.0
paths:
  /hello:
    get:
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  msg:
                    type: string
`
	uploadReq := uploadSpecRequest{
		ID:  "hello-spec",
		Raw: rawOpenAPI,
	}
	data, _ := json.Marshal(uploadReq)
	req := httptest.NewRequest("POST", "/api/specs", bytes.NewReader(data))
	w := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("failed to upload spec: %d", w.Code)
	}

	// 2. Start mock server
	startReq := mockRequest{
		ID: "hello-spec",
	}
	startData, _ := json.Marshal(startReq)
	reqStart := httptest.NewRequest("POST", "/api/mocks/start", bytes.NewReader(startData))
	wStart := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(wStart, reqStart)

	if wStart.Code != http.StatusOK {
		t.Fatalf("expected 200 OK starting mock, got %d. Body: %s", wStart.Code, wStart.Body.String())
	}

	var startResp map[string]string
	_ = json.NewDecoder(wStart.Body).Decode(&startResp)
	if startResp["status"] != "started" || startResp["address"] == "" {
		t.Fatalf("unexpected start response: %v", startResp)
	}

	mockAddr := startResp["address"]

	// 3. Make request to the mock server
	resp, err := http.Get(mockAddr + "/hello")
	if err != nil {
		t.Fatalf("failed to GET mock: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected mock to return 200, got %d", resp.StatusCode)
	}

	// 4. Stop mock server
	reqStop := httptest.NewRequest("POST", "/api/mocks/stop", bytes.NewReader(startData))
	wStop := httptest.NewRecorder()
	srv.server.Handler.ServeHTTP(wStop, reqStop)

	if wStop.Code != http.StatusOK {
		t.Fatalf("expected 200 OK stopping mock, got %d", wStop.Code)
	}

	var stopResp map[string]string
	_ = json.NewDecoder(wStop.Body).Decode(&stopResp)
	if stopResp["status"] != "stopped" {
		t.Errorf("unexpected stop response: %v", stopResp)
	}

	// 5. Verify mock is no longer reachable
	_, err = http.Get(mockAddr + "/hello")
	if err == nil {
		t.Error("expected error GETting stopped mock server, but it succeeded")
	}
}
