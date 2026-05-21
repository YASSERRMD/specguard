package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/specguard/internal/core"
)

const testContractOpenAPI = `
openapi: 3.0.0
info:
  title: Test SUT API
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

func TestRunContractChecks_ConformantSUT(t *testing.T) {
	// 1. Create a conforming SUT
	sutMux := http.NewServeMux()
	sutMux.HandleFunc("/users/123e4567-e89b-12d3-a456-426614174000", func(w http.ResponseWriter, r *http.Request) {
		// Verify query parameter exists
		if r.URL.Query().Get("version") != "1" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"missing version"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"123e4567-e89b-12d3-a456-426614174000","name":"Alice"}`))
	})

	sutServer := httptest.NewServer(sutMux)
	defer sutServer.Close()

	// 2. Load Spec
	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(testContractOpenAPI))
	if err != nil {
		t.Fatalf("failed to load spec: %v", err)
	}

	// 3. Run contract checks
	result, err := adapter.RunContractChecks(spec, sutServer.URL)
	if err != nil {
		t.Fatalf("failed to run contract checks: %v", err)
	}

	if !result.Passed {
		findingsJSON, _ := json.Marshal(result.DriftReport.Findings)
		t.Errorf("expected contract checks to pass, but failed. Findings: %s", string(findingsJSON))
	}
}

func TestRunContractChecks_DriftingSUT(t *testing.T) {
	// 1. Create a SUT returning payload that violates the schema constraints (missing required field 'name')
	sutMux := http.NewServeMux()
	sutMux.HandleFunc("/users/123e4567-e89b-12d3-a456-426614174000", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"123e4567-e89b-12d3-a456-426614174000"}`)) // Missing name
	})

	sutServer := httptest.NewServer(sutMux)
	defer sutServer.Close()

	// 2. Load Spec
	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(testContractOpenAPI))
	if err != nil {
		t.Fatalf("failed to load spec: %v", err)
	}

	// 3. Run contract checks
	result, err := adapter.RunContractChecks(spec, sutServer.URL)
	if err != nil {
		t.Fatalf("failed to run contract checks: %v", err)
	}

	if result.Passed {
		t.Errorf("expected contract checks to fail due to missing required field, but passed")
	}

	hasMissingFieldFinding := false
	for _, f := range result.DriftReport.Findings {
		if f.Location == "operations.getUser.output.200" && f.Kind == core.KindConstraintViolated {
			hasMissingFieldFinding = true
		}
	}

	if !hasMissingFieldFinding {
		findingsJSON, _ := json.Marshal(result.DriftReport.Findings)
		t.Errorf("expected constraint-violated finding for getUser.output.200, got: %s", string(findingsJSON))
	}
}

func TestRunContractChecks_UnexpectedStatusSUT(t *testing.T) {
	// 1. Create a SUT returning unexpected status code 500
	sutMux := http.NewServeMux()
	sutMux.HandleFunc("/users/123e4567-e89b-12d3-a456-426614174000", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	sutServer := httptest.NewServer(sutMux)
	defer sutServer.Close()

	// 2. Load Spec
	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(testContractOpenAPI))
	if err != nil {
		t.Fatalf("failed to load spec: %v", err)
	}

	// 3. Run contract checks
	result, err := adapter.RunContractChecks(spec, sutServer.URL)
	if err != nil {
		t.Fatalf("failed to run contract checks: %v", err)
	}

	if result.Passed {
		t.Errorf("expected contract checks to fail due to 500 status code, but passed")
	}

	hasUnexpectedStatusFinding := false
	for _, f := range result.DriftReport.Findings {
		if f.Location == "operations.getUser.output.500" && f.Kind == core.KindMissing {
			hasUnexpectedStatusFinding = true
		}
	}

	if !hasUnexpectedStatusFinding {
		findingsJSON, _ := json.Marshal(result.DriftReport.Findings)
		t.Errorf("expected missing finding for getUser.output.500, got: %s", string(findingsJSON))
	}
}
