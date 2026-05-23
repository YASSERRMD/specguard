package core

import (
	"strings"
	"testing"
)

func TestGenerateValueForSchema_NumericConstraints(t *testing.T) {
	// Integer with min/max
	sInt := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarInteger,
		Constraints: []Constraint{
			{Kind: "min", Value: "10"},
			{Kind: "max", Value: "5"},
		},
	}
	valInt := GenerateValueForSchema(sInt)
	if v, ok := valInt.(int); !ok {
		t.Errorf("expected int type, got %T", valInt)
	} else {
		// Min is 10, so val (initial 1) becomes 10.
		// Max is 5, but we check max later, so it caps at 5.
		if v != 5 {
			t.Errorf("expected 5, got %d", v)
		}
	}

	// Number with min/max
	sNum := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarNumber,
		Constraints: []Constraint{
			{Kind: "min", Value: "5.5"},
			{Kind: "max", Value: "12.2"},
		},
	}
	valNum := GenerateValueForSchema(sNum)
	if v, ok := valNum.(float64); !ok {
		t.Errorf("expected float64 type, got %T", valNum)
	} else {
		if v < 5.5 || v > 12.2 {
			t.Errorf("expected between 5.5 and 12.2, got %f", v)
		}
	}
}

func TestGenerateValueForSchema_StringConstraints(t *testing.T) {
	// String with min-length
	sMinLen := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarString,
		Constraints: []Constraint{
			{Kind: "min-length", Value: "15"},
		},
	}
	valMinLen := GenerateValueForSchema(sMinLen).(string)
	if len(valMinLen) < 15 {
		t.Errorf("expected string length at least 15, got %d (value: %s)", len(valMinLen), valMinLen)
	}

	// String with max-length
	sMaxLen := Schema{
		Type:       TypeScalar,
		ScalarType: ScalarString,
		Constraints: []Constraint{
			{Kind: "max-length", Value: "5"},
		},
	}
	valMaxLen := GenerateValueForSchema(sMaxLen).(string)
	if len(valMaxLen) > 5 {
		t.Errorf("expected string length at most 5, got %d (value: %s)", len(valMaxLen), valMaxLen)
	}

	// String with formats
	formats := map[string]string{
		"uuid":      "123e4567-e89b-12d3-a456-426614174000",
		"date-time": "2026-05-21T06:10:00Z",
		"email":     "user@example.com",
		"url":       "https://example.com",
		"ipv4":      "127.0.0.1",
		"ipv6":      "::1",
	}

	for fmtName, expected := range formats {
		sFmt := Schema{
			Type:       TypeScalar,
			ScalarType: ScalarString,
			Constraints: []Constraint{
				{Kind: "format", Value: fmtName},
			},
		}
		val := GenerateValueForSchema(sFmt).(string)
		if val != expected {
			t.Errorf("expected format %s to yield %s, got %s", fmtName, expected, val)
		}
	}

	// String with patterns
	patterns := map[string]string{
		`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`: "user@example.com",
		`^[0-9]+$`: "12345",
		`^[a-z]+$`: "abcde",
	}

	for pat, expected := range patterns {
		sPat := Schema{
			Type:       TypeScalar,
			ScalarType: ScalarString,
			Constraints: []Constraint{
				{Kind: "pattern", Value: pat},
			},
		}
		val := GenerateValueForSchema(sPat).(string)
		if !strings.Contains(val, expected) && val != expected {
			t.Errorf("expected pattern %s to match/contain %s, got %s", pat, expected, val)
		}
	}
}
