package core

import (
	"strings"
	"testing"
)

func TestMatchScalarString(t *testing.T) {
	// String without constraints
	schema := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarString,
	}

	if err := schema.Match("hello"); err != nil {
		t.Errorf("expected string to match: %v", err)
	}

	if err := schema.Match(123); err == nil {
		t.Error("expected error for non-string type")
	}
}

func TestMatchScalarStringConstraints(t *testing.T) {
	schema := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarString,
		Constraints: []Constraint{
			{Kind: "min-length", Value: "3"},
			{Kind: "max-length", Value: "10"},
			{Kind: "pattern", Value: "^[a-z]+$"},
		},
	}

	// Valid cases
	if err := schema.Match("abc"); err != nil {
		t.Errorf("expected match: %v", err)
	}
	if err := schema.Match("abcdefghij"); err != nil {
		t.Errorf("expected match: %v", err)
	}

	// Invalid: too short
	if err := schema.Match("ab"); err == nil {
		t.Error("expected error for min-length violation")
	} else if !strings.Contains(err.Error(), "minimum length constraint violated") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Invalid: too long
	if err := schema.Match("abcdefghijk"); err == nil {
		t.Error("expected error for max-length violation")
	} else if !strings.Contains(err.Error(), "maximum length constraint violated") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Invalid: pattern mismatch
	if err := schema.Match("abc1"); err == nil {
		t.Error("expected error for pattern mismatch")
	} else if !strings.Contains(err.Error(), "pattern constraint violated") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestMatchScalarFormats(t *testing.T) {
	uuidSchema := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarString,
		Constraints: []Constraint{
			{Kind: "format", Value: "uuid"},
		},
	}

	if err := uuidSchema.Match("123e4567-e89b-12d3-a456-426614174000"); err != nil {
		t.Errorf("expected valid uuid to pass: %v", err)
	}
	if err := uuidSchema.Match("invalid-uuid"); err == nil {
		t.Error("expected invalid uuid to fail")
	}

	dtSchema := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarString,
		Constraints: []Constraint{
			{Kind: "format", Value: "date-time"},
		},
	}

	if err := dtSchema.Match("2026-05-19T22:14:53Z"); err != nil {
		t.Errorf("expected RFC3339 date to pass: %v", err)
	}
	if err := dtSchema.Match("2026-05-19T22:14:53+04:00"); err != nil {
		t.Errorf("expected timezone offset date to pass: %v", err)
	}
	if err := dtSchema.Match("19-05-2026"); err == nil {
		t.Error("expected invalid date format to fail")
	}

	emailSchema := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarString,
		Constraints: []Constraint{
			{Kind: "format", Value: "email"},
		},
	}

	if err := emailSchema.Match("test@example.com"); err != nil {
		t.Errorf("expected valid email to pass: %v", err)
	}
	if err := emailSchema.Match("invalid-email"); err == nil {
		t.Error("expected invalid email to fail")
	}
}

func TestMatchScalarNumeric(t *testing.T) {
	numberSchema := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarNumber,
		Constraints: []Constraint{
			{Kind: "min", Value: "2.5"},
			{Kind: "max", Value: "10.1"},
		},
	}

	if err := numberSchema.Match(3.5); err != nil {
		t.Errorf("expected match: %v", err)
	}
	if err := numberSchema.Match(10); err != nil {
		t.Errorf("expected match: %v", err)
	}

	if err := numberSchema.Match(2.4); err == nil {
		t.Error("expected error for min constraint violation")
	}
	if err := numberSchema.Match(10.2); err == nil {
		t.Error("expected error for max constraint violation")
	}

	intSchema := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarInteger,
	}

	if err := intSchema.Match(5); err != nil {
		t.Errorf("expected match: %v", err)
	}
	if err := intSchema.Match(5.0); err != nil {
		t.Errorf("expected match: %v", err)
	}
	if err := intSchema.Match(5.5); err == nil {
		t.Error("expected fractional float to fail integer check")
	}
}

func TestMatchScalarBoolean(t *testing.T) {
	schema := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarBoolean,
	}

	if err := schema.Match(true); err != nil {
		t.Errorf("expected true to match: %v", err)
	}
	if err := schema.Match(false); err != nil {
		t.Errorf("expected false to match: %v", err)
	}
	if err := schema.Match("true"); err == nil {
		t.Error("expected string to fail boolean check")
	}
}

func TestMatchEnum(t *testing.T) {
	schema := Schema{
		Type:       TypeEnum,
		EnumValues: []string{"red", "green", "blue"},
	}

	if err := schema.Match("green"); err != nil {
		t.Errorf("expected match: %v", err)
	}
	if err := schema.Match("yellow"); err == nil {
		t.Error("expected yellow to fail enum check")
	}
}

func TestMatchArray(t *testing.T) {
	schema := Schema{
		Type: TypeArray,
		Item: &Schema{
			Type:       TypeScalar,
			ScalarType: ScalarInteger,
		},
	}

	if err := schema.Match([]interface{}{1, 2, 3}); err != nil {
		t.Errorf("expected match: %v", err)
	}
	if err := schema.Match([]int{1, 2, 3}); err != nil {
		t.Errorf("expected match for int slice: %v", err)
	}

	// Inner constraint violation
	if err := schema.Match([]interface{}{1, 2.5, 3}); err == nil {
		t.Error("expected match failure for fractional item")
	} else if !strings.Contains(err.Error(), "validation failed at \"$[1]\"") {
		t.Errorf("unexpected path in error: %v", err)
	}
}

func TestMatchObject(t *testing.T) {
	schema := Schema{
		Type:     TypeObject,
		Required: []string{"id", "name"},
		Properties: map[string]Schema{
			"id": {
				Type:       TypeScalar,
				ScalarType: ScalarString,
				Constraints: []Constraint{
					{Kind: "format", Value: "uuid"},
				},
			},
			"name": {
				Type:       TypeScalar,
				ScalarType: ScalarString,
			},
			"age": {
				Type:       TypeScalar,
				ScalarType: ScalarInteger,
			},
		},
	}

	validObj := map[string]interface{}{
		"id":   "123e4567-e89b-12d3-a456-426614174000",
		"name": "Yasser",
		"age":  28,
	}

	if err := schema.Match(validObj); err != nil {
		t.Errorf("expected object to match: %v", err)
	}

	// Missing required property
	missingRequired := map[string]interface{}{
		"id": "123e4567-e89b-12d3-a456-426614174000",
	}
	if err := schema.Match(missingRequired); err == nil {
		t.Error("expected missing required property to fail validation")
	} else if !strings.Contains(err.Error(), "validation failed at \"$.name\": expected present, got missing") {
		t.Errorf("unexpected error: %v", err)
	}

	// Type mismatch in nested field
	invalidNested := map[string]interface{}{
		"id":   "invalid-uuid",
		"name": "Yasser",
	}
	if err := schema.Match(invalidNested); err == nil {
		t.Error("expected invalid UUID to fail validation")
	} else if !strings.Contains(err.Error(), "validation failed at \"$.id\"") {
		t.Errorf("unexpected error: %v", err)
	}
}
