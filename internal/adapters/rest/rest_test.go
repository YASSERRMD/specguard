package rest

import (
	"testing"

	"github.com/YASSERRMD/specguard/internal/core"
)

const validOpenAPISpec = `
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
        - name: fields
          in: query
          required: false
          schema:
            type: string
      responses:
        '200':
          description: Success
          content:
            application/json:
              schema:
                type: object
                required:
                  - name
                  - age
                properties:
                  name:
                    type: string
                    minLength: 2
                  age:
                    type: integer
                    minimum: 0
                  status:
                    type: string
                    enum: [active, inactive]
        '404':
          description: User not found
          content:
            application/json:
              schema:
                type: object
                required:
                  - error
                properties:
                  error:
                    type: string
`

const malformedOpenAPISpec = `
openapi: 3.0.0
info:
  title: Test API
paths:
  /users:
    get:
      parameters:
        - name: id
          in: invalid_location
`

func TestAdapter_LoadSpec_Valid(t *testing.T) {
	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(validOpenAPISpec))
	if err != nil {
		t.Fatalf("Expected no error loading valid spec, got: %v", err)
	}

	if spec == nil {
		t.Fatal("Expected non-nil spec")
	}

	op, exists := spec.Operations["getUser"]
	if !exists {
		t.Fatalf("Expected operation 'getUser' to exist in parsed spec")
	}

	// Verify metadata
	if op.Metadata["path"] != "/users/{id}" {
		t.Errorf("Expected path metadata '/users/{id}', got: %q", op.Metadata["path"])
	}
	if op.Metadata["method"] != "GET" {
		t.Errorf("Expected method metadata 'GET', got: %q", op.Metadata["method"])
	}

	// Verify path parameter and constraint
	pathSchema, exists := op.Input.Properties["path"]
	if !exists {
		t.Fatal("Expected path properties to exist in input schema")
	}
	idSchema, exists := pathSchema.Properties["id"]
	if !exists {
		t.Fatal("Expected 'id' path parameter to exist")
	}
	if idSchema.Type != core.TypeScalar || idSchema.ScalarType != core.ScalarString {
		t.Errorf("Expected 'id' parameter to be string scalar, got: type=%s, scalar=%s", idSchema.Type, idSchema.ScalarType)
	}
	hasUUIDFormat := false
	for _, c := range idSchema.Constraints {
		if c.Kind == "format" && c.Value == "uuid" {
			hasUUIDFormat = true
		}
	}
	if !hasUUIDFormat {
		t.Error("Expected 'id' parameter to have format uuid constraint")
	}

	// Verify output status 200 shape
	successSchema, exists := op.Output.Properties["200"]
	if !exists {
		t.Fatal("Expected 200 output schema to exist")
	}
	if successSchema.Type != core.TypeObject {
		t.Errorf("Expected 200 response schema to be object, got: %s", successSchema.Type)
	}
	nameSchema, exists := successSchema.Properties["name"]
	if !exists {
		t.Fatal("Expected 'name' property to exist in success schema")
	}
	hasMinLength := false
	for _, c := range nameSchema.Constraints {
		if c.Kind == "min-length" && c.Value == "2" {
			hasMinLength = true
		}
	}
	if !hasMinLength {
		t.Error("Expected 'name' property to have min-length constraint of 2")
	}

	statusSchema, exists := successSchema.Properties["status"]
	if !exists {
		t.Fatal("Expected 'status' property to exist in success schema")
	}
	if statusSchema.Type != core.TypeEnum {
		t.Errorf("Expected 'status' property to be type enum, got: %s", statusSchema.Type)
	}
	if len(statusSchema.EnumValues) != 2 || statusSchema.EnumValues[0] != "active" || statusSchema.EnumValues[1] != "inactive" {
		t.Errorf("Expected status enum values [active, inactive], got: %v", statusSchema.EnumValues)
	}

	// Verify error status 404 shape
	notFoundSchema, exists := op.ErrorShapes["404"]
	if !exists {
		t.Fatal("Expected 404 error schema to exist")
	}
	if notFoundSchema.Type != core.TypeObject {
		t.Errorf("Expected 404 error schema to be object, got: %s", notFoundSchema.Type)
	}
}

func TestAdapter_LoadSpec_Malformed(t *testing.T) {
	adapter := NewAdapter()
	_, err := adapter.LoadSpec([]byte(malformedOpenAPISpec))
	if err == nil {
		t.Fatal("Expected error loading malformed spec, got nil")
	}

	// Ensure error mentions parsing or validation issue
	errStr := err.Error()
	if errStr == "" {
		t.Error("Expected non-empty error message")
	}
}
