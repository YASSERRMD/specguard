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

func TestMockServerStatefulCRUD(t *testing.T) {
	rawOpenAPI := `
openapi: 3.0.3
info:
  title: Stateful Pets API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: List all pets
      operationId: listPets
      parameters:
        - name: limit
          in: query
          required: false
          schema:
            type: integer
        - name: offset
          in: query
          required: false
          schema:
            type: integer
      responses:
        '200':
          description: A list of pets
          content:
            application/json:
              schema:
                type: object
                required:
                  - total
                  - pets
                properties:
                  total:
                    type: integer
                  limit:
                    type: integer
                  offset:
                    type: integer
                  pets:
                    type: array
                    items:
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
                        tag:
                          type: string
    post:
      summary: Create a pet
      operationId: createPet
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required:
                - name
              properties:
                name:
                  type: string
                tag:
                  type: string
      responses:
        '201':
          description: Created pet
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
                  tag:
                    type: string
  /pets/{id}:
    get:
      summary: Info for a specific pet
      operationId: showPetById
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
            format: uuid
      responses:
        '200':
          description: Expected response to a valid request
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
                  tag:
                    type: string
        '404':
          description: Pet not found
          content:
            application/json:
              schema:
                type: object
                required:
                  - message
                properties:
                  message:
                    type: string
    put:
      summary: Update a pet
      operationId: updatePet
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
            format: uuid
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required:
                - name
              properties:
                name:
                  type: string
                tag:
                  type: string
      responses:
        '200':
          description: Updated pet details
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
                  tag:
                    type: string
    delete:
      summary: Delete a pet
      operationId: deletePet
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
            format: uuid
      responses:
        '204':
          description: Pet deleted
`

	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(rawOpenAPI))
	if err != nil {
		t.Fatalf("failed to load specification: %v", err)
	}

	config := core.MockConfig{
		Host: "127.0.0.1",
		Port: 0,
	}

	mockServer, err := adapter.GenerateMock(spec, config)
	if err != nil {
		t.Fatalf("failed to generate mock: %v", err)
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

	// 1. Initial GET on collection (since uninitialized, falls back to static mock list generation)
	resp, err := client.Get(addr + "/pets")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from uninitialized GET list, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. POST (Create Fido)
	fidoBody := `{"name": "Fido", "tag": "dog"}`
	resp, err = client.Post(addr+"/pets", "application/json", strings.NewReader(fidoBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		t.Errorf("expected 200 or 201, got %d", resp.StatusCode)
	}
	var fido map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&fido)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	fidoId, ok := fido["id"].(string)
	if !ok || fidoId == "" {
		t.Fatalf("expected string id in response, got %v", fido["id"])
	}
	if fido["name"] != "Fido" || fido["tag"] != "dog" {
		t.Errorf("expected Fido values, got %v", fido)
	}

	// 3. POST (Create Whiskers)
	whiskersBody := `{"name": "Whiskers", "tag": "cat"}`
	resp, err = client.Post(addr+"/pets", "application/json", strings.NewReader(whiskersBody))
	if err != nil {
		t.Fatal(err)
	}
	var whiskers map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&whiskers)
	resp.Body.Close()
	_ = whiskers["id"].(string)

	// 4. GET collection (List pets)
	resp, err = client.Get(addr + "/pets")
	if err != nil {
		t.Fatal(err)
	}
	var listResp map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&listResp)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	total, ok := listResp["total"].(float64)
	if !ok || total != 2 {
		t.Errorf("expected total 2, got %v", listResp["total"])
	}
	petsList, ok := listResp["pets"].([]interface{})
	if !ok || len(petsList) != 2 {
		t.Errorf("expected 2 pets in list, got %v", listResp["pets"])
	}

	// 5. GET collection with pagination limit=1
	resp, err = client.Get(addr + "/pets?limit=1")
	if err != nil {
		t.Fatal(err)
	}
	var pageResp map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&pageResp)
	resp.Body.Close()
	pagePets := pageResp["pets"].([]interface{})
	if len(pagePets) != 1 {
		t.Errorf("expected 1 pet under limit=1, got %d", len(pagePets))
	}
	if pageResp["total"].(float64) != 2 {
		t.Errorf("expected total 2 metadata, got %v", pageResp["total"])
	}

	// 6. GET member (retrieve Fido)
	resp, err = client.Get(addr + "/pets/" + fidoId)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK retrieving Fido, got %d", resp.StatusCode)
	}
	var gotFido map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&gotFido)
	resp.Body.Close()
	if gotFido["name"] != "Fido" {
		t.Errorf("expected Fido, got %v", gotFido["name"])
	}

	// 7. GET member (retrieve non-existent)
	resp, err = client.Get(addr + "/pets/123e4567-e89b-12d3-a456-426614174999")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 8. PUT member (update Fido)
	fidoUpdateBody := `{"name": "Fido Updated", "tag": "dog-breed"}`
	req, err := http.NewRequest(http.MethodPut, addr+"/pets/"+fidoId, strings.NewReader(fidoUpdateBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK updating Fido, got %d", resp.StatusCode)
	}
	var updatedFido map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&updatedFido)
	resp.Body.Close()
	if updatedFido["name"] != "Fido Updated" || updatedFido["id"] != fidoId {
		t.Errorf("expected updated details and same ID, got %v", updatedFido)
	}

	// Verify update persists
	resp, err = client.Get(addr + "/pets/" + fidoId)
	if err != nil {
		t.Fatal(err)
	}
	var doubleCheckFido map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&doubleCheckFido)
	resp.Body.Close()
	if doubleCheckFido["name"] != "Fido Updated" {
		t.Errorf("expected name to be updated in storage, got %v", doubleCheckFido["name"])
	}

	// 9. DELETE member (delete Fido)
	req, err = http.NewRequest(http.MethodDelete, addr+"/pets/"+fidoId, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Errorf("expected 204 or 200 on delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Assert GET returns 404
	resp, err = client.Get(addr + "/pets/" + fidoId)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for deleted Fido, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Assert collection list total is 1 (Whiskers remains)
	resp, err = client.Get(addr + "/pets")
	if err != nil {
		t.Fatal(err)
	}
	var listAfterDelete map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&listAfterDelete)
	resp.Body.Close()
	if listAfterDelete["total"].(float64) != 1 {
		t.Errorf("expected total 1 after delete, got %v", listAfterDelete["total"])
	}

	// 10. Reset State
	resp, err = client.Post(addr+"/__reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from reset, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify collection list resets to uninitialized/default list
	resp, err = client.Get(addr + "/pets")
	if err != nil {
		t.Fatal(err)
	}
	var listAfterReset map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&listAfterReset)
	resp.Body.Close()
	// Since it's reset, it should fall back to generating default mock elements
	if listAfterReset["total"] == nil {
		t.Errorf("expected mock output schema to be generated, got nil total")
	}
}

func TestMockServerScenarios(t *testing.T) {
	rawOpenAPI := `
openapi: 3.0.3
info:
  title: Scenario Pets API
  version: 1.0.0
paths:
  /pets:
    get:
      summary: List all pets
      operationId: listPets
      responses:
        '200':
          description: A list of pets
          content:
            application/json:
              schema:
                type: object
                required:
                  - total
                  - pets
                properties:
                  total:
                    type: integer
                  pets:
                    type: array
                    items:
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
    post:
      summary: Create a pet
      operationId: createPet
      responses:
        '201':
          description: Created pet
          content:
            application/json:
              schema:
                type: object
                required:
                  - id
                properties:
                  id:
                    type: string
                    format: uuid
  /pets/{id}:
    get:
      summary: Info for a specific pet
      operationId: showPetById
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
            format: uuid
      responses:
        '200':
          description: Expected response
          content:
            application/json:
              schema:
                type: object
                required:
                  - id
                properties:
                  id:
                    type: string
                    format: uuid
        '404':
          description: Pet not found
          content:
            application/json:
              schema:
                type: object
                required:
                  - message
                properties:
                  message:
                    type: string
`

	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(rawOpenAPI))
	if err != nil {
		t.Fatalf("failed to load specification: %v", err)
	}

	// Seed some spec metadata for custom scenario
	createPetOp := spec.Operations["createPet"]
	if createPetOp.Metadata == nil {
		createPetOp.Metadata = make(map[string]string)
	}
	createPetOp.Metadata["scenario:metadata-tea:status"] = "418"
	createPetOp.Metadata["scenario:metadata-tea:body"] = `{"message": "Metadata Teapot"}`
	createPetOp.Metadata["scenario:metadata-tea:headers"] = `{"X-Metadata-Tea": "yes"}`
	spec.Operations["createPet"] = createPetOp

	config := core.MockConfig{
		Host: "127.0.0.1",
		Port: 0,
		ProtocolConfig: map[string]interface{}{
			"scenarios": map[string]interface{}{
				"teapot": map[string]interface{}{
					"status": float64(418),
					"body": map[string]interface{}{
						"message": "I am a teapot",
					},
					"headers": map[string]interface{}{
						"X-Teapot": "true",
					},
				},
			},
			"operations": map[string]interface{}{
				"listPets": map[string]interface{}{
					"scenarios": map[string]interface{}{
						"busy": map[string]interface{}{
							"status": float64(503),
							"body": map[string]interface{}{
								"message": "Service Busy",
							},
						},
					},
				},
			},
		},
	}

	mockServer, err := adapter.GenerateMock(spec, config)
	if err != nil {
		t.Fatalf("failed to generate mock: %v", err)
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

	// 1. Built-in "not-found" scenario (query param)
	resp, err := client.Get(addr + "/pets/123e4567-e89b-12d3-a456-426614174000?_scenario=not-found")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	var errBody map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	resp.Body.Close()
	if errBody["message"] != "mock_string" {
		t.Errorf("expected shape conforming error message, got %v", errBody["message"])
	}

	// 2. Built-in "server-error" scenario (header)
	req, err := http.NewRequest(http.MethodGet, addr+"/pets/123e4567-e89b-12d3-a456-426614174000", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Mock-Scenario", "server-error")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 3. Built-in "empty-result" scenario (query param)
	resp, err = client.Get(addr + "/pets?_scenario=empty-result")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	var emptyResp map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&emptyResp)
	resp.Body.Close()
	petsList, ok := emptyResp["pets"].([]interface{})
	if !ok || len(petsList) != 0 {
		t.Errorf("expected empty array, got %v", emptyResp["pets"])
	}
	if emptyResp["total"].(float64) != 0 {
		t.Errorf("expected total 0, got %v", emptyResp["total"])
	}

	// 4. Config-defined global scenario ("teapot")
	resp, err = client.Get(addr + "/pets?_scenario=teapot")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 418 {
		t.Errorf("expected 418, got %d", resp.StatusCode)
	}
	var teapotBody map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&teapotBody)
	resp.Body.Close()
	if teapotBody["message"] != "I am a teapot" {
		t.Errorf("expected I am a teapot, got %v", teapotBody["message"])
	}
	if resp.Header.Get("X-Teapot") != "true" {
		t.Errorf("expected X-Teapot header to be true, got %v", resp.Header.Get("X-Teapot"))
	}

	// 5. Config-defined operation scenario ("busy" on listPets)
	resp, err = client.Get(addr + "/pets?_scenario=busy")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 503 {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}
	var busyBody map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&busyBody)
	resp.Body.Close()
	if busyBody["message"] != "Service Busy" {
		t.Errorf("expected Service Busy, got %v", busyBody["message"])
	}

	// 6. Spec metadata-defined scenario ("metadata-tea" on createPet)
	resp, err = client.Post(addr+"/pets?_scenario=metadata-tea", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 418 {
		t.Errorf("expected 418, got %d", resp.StatusCode)
	}
	var specTeaBody map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&specTeaBody)
	resp.Body.Close()
	if specTeaBody["message"] != "Metadata Teapot" {
		t.Errorf("expected Metadata Teapot, got %v", specTeaBody["message"])
	}
	if resp.Header.Get("X-Metadata-Tea") != "yes" {
		t.Errorf("expected X-Metadata-Tea header to be yes, got %v", resp.Header.Get("X-Metadata-Tea"))
	}
}
