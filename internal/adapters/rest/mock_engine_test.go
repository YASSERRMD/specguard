package rest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/YASSERRMD/specguard/internal/core"
)

func TestMockServerIntegration(t *testing.T) {
	// 1. Define a comprehensive OpenAPI spec to load
	rawOpenAPI := `
openapi: 3.0.3
info:
  title: Test API
  version: 1.0.0
paths:
  /users/{id}:
    get:
      summary: Get a user by ID
      operationId: getUser
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
            format: uuid
        - name: include_details
          in: query
          required: false
          schema:
            type: boolean
        - name: X-Test-Header
          in: header
          required: true
          schema:
            type: integer
            minimum: 10
      responses:
        '200':
          description: User details
          content:
            application/json:
              schema:
                type: object
                required:
                  - id
                  - name
                  - email
                  - status
                properties:
                  id:
                    type: string
                    format: uuid
                  name:
                    type: string
                  email:
                    type: string
                    format: email
                  status:
                    type: string
                    enum: [active, inactive]
                  age:
                    type: integer
                    minimum: 18
  /users:
    post:
      summary: Create a user
      operationId: createUser
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required:
                - name
                - email
              properties:
                name:
                  type: string
                email:
                  type: string
                  format: email
      responses:
        '201':
          description: Created user
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

	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(rawOpenAPI))
	if err != nil {
		t.Fatalf("failed to load test specification: %v", err)
	}

	// 2. Start mock server
	config := core.MockConfig{
		Host: "127.0.0.1",
		Port: 0, // Random port
	}

	mockServer, err := adapter.GenerateMock(spec, config)
	if err != nil {
		t.Fatalf("failed to generate mock server: %v", err)
	}

	err = mockServer.Start()
	if err != nil {
		t.Fatalf("failed to start mock server: %v", err)
	}
	defer func() {
		_ = mockServer.Stop()
	}()

	addr := mockServer.GetAddress()
	client := &http.Client{}

	// Test case 1: Successful GET request matching constraints
	req, err := http.NewRequest("GET", addr+"/users/123e4567-e89b-12d3-a456-426614174000?include_details=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Header", "15")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200 OK, got %d. Body: %s", resp.StatusCode, string(body))
	} else {
		// Parse and validate response structure matches schemas
		var respBody map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&respBody)
		if err != nil {
			t.Fatalf("failed to decode JSON response: %v", err)
		}

		if respBody["id"] != "123e4567-e89b-12d3-a456-426614174000" {
			t.Errorf("expected generated UUID, got %v", respBody["id"])
		}
		if respBody["email"] != "mock@example.com" {
			t.Errorf("expected generated email, got %v", respBody["email"])
		}
		if respBody["status"] != "active" {
			t.Errorf("expected first enum value active, got %v", respBody["status"])
		}
		if age, ok := respBody["age"].(float64); !ok || age < 18 {
			t.Errorf("expected age >= 18, got %v", respBody["age"])
		}
	}

	// Test case 2: Validation failure due to invalid path param UUID
	req, err = http.NewRequest("GET", addr+"/users/not-a-uuid?include_details=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Header", "15")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", resp.StatusCode)
	} else {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if !strings.Contains(errResp["error"], "uuid constraint violated") {
			t.Errorf("expected uuid constraint violation error, got: %s", errResp["error"])
		}
	}

	// Test case 3: Validation failure due to invalid header format/minimum
	req, err = http.NewRequest("GET", addr+"/users/123e4567-e89b-12d3-a456-426614174000?include_details=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Header", "5") // less than minimum 10

	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", resp.StatusCode)
	}

	// Test case 4: Validation failure due to invalid query type
	req, err = http.NewRequest("GET", addr+"/users/123e4567-e89b-12d3-a456-426614174000?include_details=not-a-bool", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Header", "15")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", resp.StatusCode)
	}

	// Test case 5: Successful POST request with JSON body
	bodyJSON := `{"name": "Alice", "email": "alice@gmail.com"}`
	req, err = http.NewRequest("POST", addr+"/users", bytes.NewReader([]byte(bodyJSON)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 Created, got %d", resp.StatusCode)
	}

	// Test case 6: Validation failure on POST request body constraints
	invalidBodyJSON := `{"name": "Alice", "email": "not-an-email"}`
	req, err = http.NewRequest("POST", addr+"/users", bytes.NewReader([]byte(invalidBodyJSON)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", resp.StatusCode)
	}

	// Test case 7: Route not found (404)
	req, err = http.NewRequest("GET", addr+"/not-exists", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", resp.StatusCode)
	}
}
