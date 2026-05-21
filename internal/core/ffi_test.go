package core

import (
	"encoding/json"
	"testing"
)

func TestHashSpec_Determinism(t *testing.T) {
	// Construct Spec A
	specA := &NormalizedSpec{
		Operations: map[string]Operation{
			"getUser": {
				ID: "getUser",
				Input: Schema{
					Type: TypeObject,
					Properties: map[string]Schema{
						"id": {
							Type:       TypeScalar,
							ScalarType: ScalarInteger,
						},
					},
					Required: []string{"id"},
				},
				Output: Schema{
					Type: TypeObject,
					Properties: map[string]Schema{
						"name": {
							Type:       TypeScalar,
							ScalarType: ScalarString,
						},
					},
				},
				Metadata: map[string]string{
					"method":      "GET",
					"path":        "/users/{id}",
					"description": "Get user details",
				},
			},
		},
	}

	// Construct Spec B which is structurally identical but has:
	// 1. Different description/metadata (ignored in structural representation except method/path)
	// 2. Different insertion order (automatically sorted by BTreeMap in Rust)
	specB := &NormalizedSpec{
		Operations: map[string]Operation{
			"getUser": {
				ID: "getUser",
				Input: Schema{
					Type: TypeObject,
					Properties: map[string]Schema{
						"id": {
							Type:       TypeScalar,
							ScalarType: ScalarInteger,
						},
					},
					Required: []string{"id"},
				},
				Output: Schema{
					Type: TypeObject,
					Properties: map[string]Schema{
						"name": {
							Type:       TypeScalar,
							ScalarType: ScalarString,
						},
					},
				},
				Metadata: map[string]string{
					"method":      "GET",
					"path":        "/users/{id}",
					"description": "DIFFERENT DESCRIPTION - SHOULD NOT AFFECT STRUCTURAL HASH",
				},
			},
		},
	}

	hashA, err := HashSpec(specA)
	if err != nil {
		t.Fatalf("failed to hash spec A: %v", err)
	}

	hashB, err := HashSpec(specB)
	if err != nil {
		t.Fatalf("failed to hash spec B: %v", err)
	}

	if hashA != hashB {
		t.Errorf("expected deterministic structural hashes to match, got A=%s, B=%s", hashA, hashB)
	}

	// Construct Spec C which has a structural difference (changed type of id property)
	specC := &NormalizedSpec{
		Operations: map[string]Operation{
			"getUser": {
				ID: "getUser",
				Input: Schema{
					Type: TypeObject,
					Properties: map[string]Schema{
						"id": {
							Type:       TypeScalar,
							ScalarType: ScalarString, // Type changed integer -> string
						},
					},
					Required: []string{"id"},
				},
				Output: Schema{
					Type: TypeObject,
					Properties: map[string]Schema{
						"name": {
							Type:       TypeScalar,
							ScalarType: ScalarString,
						},
					},
				},
				Metadata: map[string]string{
					"method":      "GET",
					"path":        "/users/{id}",
					"description": "Get user details",
				},
			},
		},
	}

	hashC, err := HashSpec(specC)
	if err != nil {
		t.Fatalf("failed to hash spec C: %v", err)
	}

	if hashA == hashC {
		t.Errorf("expected hashes of structurally different specs to differ, both got %s", hashA)
	}
}

func TestDiffSpecs(t *testing.T) {
	specA := &NormalizedSpec{
		Operations: map[string]Operation{
			"getUser": {
				ID: "getUser",
				Input: Schema{
					Type: TypeObject,
					Properties: map[string]Schema{
						"id": {
							Type:       TypeScalar,
							ScalarType: ScalarInteger,
						},
					},
					Required: []string{"id"},
				},
				Output: Schema{
					Type: TypeObject,
					Properties: map[string]Schema{
						"name": {
							Type:       TypeScalar,
							ScalarType: ScalarString,
						},
					},
				},
				Metadata: map[string]string{
					"method": "GET",
					"path":   "/users/{id}",
				},
			},
		},
	}

	specB := &NormalizedSpec{
		Operations: map[string]Operation{
			"getUser": {
				ID: "getUser",
				Input: Schema{
					Type: TypeObject,
					Properties: map[string]Schema{
						"id": {
							Type:       TypeScalar,
							ScalarType: ScalarString, // Type changed
						},
						"version": { // Added property
							Type:       TypeScalar,
							ScalarType: ScalarString,
						},
					},
					Required: []string{"id"},
				},
				Output: Schema{
					Type: TypeObject,
					Properties: map[string]Schema{
						"name": {
							Type:       TypeScalar,
							ScalarType: ScalarString,
						},
					},
				},
				Metadata: map[string]string{
					"method": "GET",
					"path":   "/users/{id}",
				},
			},
			"createUser": { // Added operation
				ID: "createUser",
				Input: Schema{
					Type: TypeObject,
				},
				Output: Schema{
					Type: TypeObject,
				},
				Metadata: map[string]string{
					"method": "POST",
					"path":   "/users",
				},
			},
		},
	}

	report, err := DiffSpecs(specA, specB)
	if err != nil {
		t.Fatalf("failed to compute diff: %v", err)
	}

	findingsJSON, _ := json.Marshal(report.Findings)
	t.Logf("Findings: %s", string(findingsJSON))

	// We expect:
	// 1. type-changed on input id
	// 2. added input property version
	// 3. added operation createUser
	hasTypeChange := false
	hasAddedProp := false
	hasAddedOp := false

	for _, f := range report.Findings {
		switch f.Location {
		case "operations.getUser.input.properties.id.scalar_type":
			if f.Kind == "type-changed" && f.Expected == "integer" && f.Actual == "string" && f.Severity == "error" {
				hasTypeChange = true
			}
		case "operations.getUser.input.properties.version":
			if f.Kind == "added" && f.Severity == "info" {
				hasAddedProp = true
			}
		case "operations":
			if f.Kind == "added" && f.Actual == "operation createUser" && f.Severity == "info" {
				hasAddedOp = true
			}
		}
	}

	if !hasTypeChange {
		t.Errorf("expected type-changed finding for getUser.input.id")
	}
	if !hasAddedProp {
		t.Errorf("expected added property finding for getUser.input.version")
	}
	if !hasAddedOp {
		t.Errorf("expected added operation finding for createUser")
	}
}
