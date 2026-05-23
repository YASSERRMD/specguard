package core

import (
	"strconv"
	"strings"
)

// GenerateValueForSchema recursively creates a valid mock/artificial value
// that conforms to the given Schema and its validation constraints.
func GenerateValueForSchema(s Schema) interface{} {
	switch s.Type {
	case TypeScalar:
		switch s.ScalarType {
		case ScalarInteger:
			val := 1
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if v, err := strconv.Atoi(c.Value); err == nil && val < v {
						val = v
					}
				}
				if c.Kind == "max" {
					if v, err := strconv.Atoi(c.Value); err == nil && val > v {
						val = v
					}
				}
			}
			return val
		case ScalarNumber:
			val := 1.0
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if v, err := strconv.ParseFloat(c.Value, 64); err == nil && val < v {
						val = v
					}
				}
				if c.Kind == "max" {
					if v, err := strconv.ParseFloat(c.Value, 64); err == nil && val > v {
						val = v
					}
				}
			}
			return val
		case ScalarBoolean:
			return true
		case ScalarString:
			// Check format first
			for _, c := range s.Constraints {
				if c.Kind == "format" {
					switch c.Value {
					case "uuid":
						return "123e4567-e89b-12d3-a456-426614174000"
					case "date-time":
						return "2026-05-21T06:10:00Z"
					case "email":
						return "user@example.com"
					case "uri", "url":
						return "https://example.com"
					case "ipv4":
						return "127.0.0.1"
					case "ipv6":
						return "::1"
					}
				}
			}

			// Check pattern next
			for _, c := range s.Constraints {
				if c.Kind == "pattern" {
					if strings.Contains(c.Value, "@") {
						return "user@example.com"
					}
					if strings.Contains(c.Value, "0-9") || strings.Contains(c.Value, "\\d") {
						return "12345"
					}
					if strings.Contains(c.Value, "a-z") {
						return "abcde"
					}
				}
			}

			val := "mock_value"

			// Handle min-length and max-length
			var minLen, maxLen int
			hasMin, hasMax := false, false
			for _, c := range s.Constraints {
				if c.Kind == "min-length" {
					if v, err := strconv.Atoi(c.Value); err == nil {
						minLen = v
						hasMin = true
					}
				}
				if c.Kind == "max-length" {
					if v, err := strconv.Atoi(c.Value); err == nil {
						maxLen = v
						hasMax = true
					}
				}
			}

			if hasMin && len(val) < minLen {
				val = val + strings.Repeat("a", minLen-len(val))
			}
			if hasMax && len(val) > maxLen {
				val = val[:maxLen]
			}
			return val
		default:
			return "mock_value"
		}
	case TypeEnum:
		if len(s.EnumValues) > 0 {
			return s.EnumValues[0]
		}
		return "enum_default"
	case TypeArray:
		if s.Item != nil {
			return []interface{}{GenerateValueForSchema(*s.Item)}
		}
		return []interface{}{}
	case TypeObject:
		res := make(map[string]interface{})
		for propName, propSchema := range s.Properties {
			res[propName] = GenerateValueForSchema(propSchema)
		}
		return res
	default:
		return nil
	}
}
