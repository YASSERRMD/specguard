package core

import (
	"strconv"
	"strings"
)

// GenerateValueForSchema recursively creates a valid mock/artificial value
// that conforms to the given Schema and its validation constraints.
func GenerateValueForSchema(s Schema) interface{} {
	if s.Example != nil {
		return s.Example
	}
	if s.Default != nil {
		return s.Default
	}
	if len(s.OneOf) > 0 {
		return GenerateValueForSchema(s.OneOf[0])
	}
	if len(s.AnyOf) > 0 {
		return GenerateValueForSchema(s.AnyOf[0])
	}
	if len(s.AllOf) > 0 {
		return GenerateValueForSchema(s.AllOf[0])
	}

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
			if propName == "*" {
				res["some_key"] = GenerateValueForSchema(propSchema)
				continue
			}
			res[propName] = GenerateValueForSchema(propSchema)
		}
		if s.AdditionalProperties != nil {
			res["additional_key"] = GenerateValueForSchema(*s.AdditionalProperties)
		}
		return res
	default:
		return nil
	}
}

// GenerateEdgeCaseValueForSchema generates boundary/edge-case values for a schema.
func GenerateEdgeCaseValueForSchema(s Schema) interface{} {
	if s.Example != nil {
		return s.Example
	}
	if s.Default != nil {
		return s.Default
	}
	if len(s.OneOf) > 0 {
		return GenerateEdgeCaseValueForSchema(s.OneOf[0])
	}
	if len(s.AnyOf) > 0 {
		return GenerateEdgeCaseValueForSchema(s.AnyOf[0])
	}
	if len(s.AllOf) > 0 {
		return GenerateEdgeCaseValueForSchema(s.AllOf[0])
	}

	switch s.Type {
	case TypeScalar:
		switch s.ScalarType {
		case ScalarInteger:
			val := 0
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if v, err := strconv.Atoi(c.Value); err == nil {
						val = v
					}
				}
				if c.Kind == "max" {
					if v, err := strconv.Atoi(c.Value); err == nil {
						val = v
					}
				}
			}
			return val
		case ScalarNumber:
			val := 0.0
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if v, err := strconv.ParseFloat(c.Value, 64); err == nil {
						val = v
					}
				}
				if c.Kind == "max" {
					if v, err := strconv.ParseFloat(c.Value, 64); err == nil {
						val = v
					}
				}
			}
			return val
		case ScalarBoolean:
			return false
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
			if hasMin {
				return strings.Repeat("a", minLen)
			}
			if hasMax {
				return strings.Repeat("a", maxLen)
			}
			return ""
		default:
			return ""
		}
	case TypeEnum:
		if len(s.EnumValues) > 0 {
			return s.EnumValues[len(s.EnumValues)-1]
		}
		return "enum_edge"
	case TypeArray:
		return []interface{}{}
	case TypeObject:
		res := make(map[string]interface{})
		for propName, propSchema := range s.Properties {
			if propName == "*" {
				res["some_key"] = GenerateEdgeCaseValueForSchema(propSchema)
				continue
			}
			res[propName] = GenerateEdgeCaseValueForSchema(propSchema)
		}
		if s.AdditionalProperties != nil {
			res["additional_key"] = GenerateEdgeCaseValueForSchema(*s.AdditionalProperties)
		}
		return res
	default:
		return nil
	}
}

// GenerateInvalidValueForSchema generates values specifically violating constraints.
func GenerateInvalidValueForSchema(s Schema) interface{} {
	if len(s.OneOf) > 0 {
		return GenerateInvalidValueForSchema(s.OneOf[0])
	}
	if len(s.AnyOf) > 0 {
		return GenerateInvalidValueForSchema(s.AnyOf[0])
	}
	if len(s.AllOf) > 0 {
		return GenerateInvalidValueForSchema(s.AllOf[0])
	}

	switch s.Type {
	case TypeScalar:
		switch s.ScalarType {
		case ScalarInteger:
			val := -999999
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if v, err := strconv.Atoi(c.Value); err == nil {
						val = v - 1
					}
				}
			}
			return val
		case ScalarNumber:
			val := -999999.0
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if v, err := strconv.ParseFloat(c.Value, 64); err == nil {
						val = v - 1.0
					}
				}
			}
			return val
		case ScalarBoolean:
			return false // must conform to boolean type for unmarshaling
		case ScalarString:
			for _, c := range s.Constraints {
				if c.Kind == "format" {
					return "invalid-format-value"
				}
				if c.Kind == "pattern" {
					return "invalid-pattern-value-does-not-match"
				}
				if c.Kind == "min-length" {
					if v, err := strconv.Atoi(c.Value); err == nil && v > 0 {
						return strings.Repeat("a", v-1)
					}
				}
				if c.Kind == "max-length" {
					if v, err := strconv.Atoi(c.Value); err == nil {
						return strings.Repeat("a", v+1)
					}
				}
			}
			return "invalid_string_value"
		default:
			return "invalid_string_value"
		}
	case TypeEnum:
		return "invalid_enum_value_not_in_list"
	case TypeArray:
		if s.Item != nil {
			return []interface{}{GenerateInvalidValueForSchema(*s.Item)}
		}
		return []interface{}{}
	case TypeObject:
		res := make(map[string]interface{})
		hasOmittedRequired := false
		for propName, propSchema := range s.Properties {
			if propName == "*" {
				res["some_key"] = GenerateInvalidValueForSchema(propSchema)
				continue
			}
			isRequired := false
			for _, r := range s.Required {
				if r == propName {
					isRequired = true
					break
				}
			}
			if isRequired && !hasOmittedRequired {
				hasOmittedRequired = true
				continue
			}
			res[propName] = GenerateValueForSchema(propSchema)
		}
		if !hasOmittedRequired && len(s.Properties) > 0 {
			for propName, propSchema := range s.Properties {
				if propName == "*" {
					res["some_key"] = GenerateInvalidValueForSchema(propSchema)
				} else {
					res[propName] = GenerateInvalidValueForSchema(propSchema)
				}
				break
			}
		}
		return res
	default:
		return nil
	}
}
