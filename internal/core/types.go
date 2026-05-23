package core

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"time"
)

// SchemaType defines the high-level data types supported.
type SchemaType string

const (
	TypeObject SchemaType = "object"
	TypeArray  SchemaType = "array"
	TypeScalar SchemaType = "scalar"
	TypeEnum   SchemaType = "enum"
)

// ScalarType defines the types of scalar values supported.
type ScalarType string

const (
	ScalarString  ScalarType = "string"
	ScalarNumber  ScalarType = "number"
	ScalarInteger ScalarType = "integer"
	ScalarBoolean ScalarType = "boolean"
)

// Constraint defines validation rules for scalars.
type Constraint struct {
	Kind  string `json:"kind"`  // "pattern", "format", "min", "max", "min-length", "max-length"
	Value string `json:"value"` // parameter for validation
}

// Schema represents a recursive protocol-neutral data shape.
type Schema struct {
	Type        SchemaType        `json:"type"`
	Properties  map[string]Schema `json:"properties,omitempty"`
	Required    []string          `json:"required,omitempty"`
	Item        *Schema           `json:"item,omitempty"`
	ScalarType  ScalarType        `json:"scalar_type,omitempty"`
	Constraints []Constraint      `json:"constraints,omitempty"`
	EnumValues  []string          `json:"enum_values,omitempty"`
}

// Operation defines a protocol-neutral API endpoint.
type Operation struct {
	ID          string            `json:"id"`
	Input       Schema            `json:"input"`
	Output      Schema            `json:"output"`
	ErrorShapes map[string]Schema `json:"error_shapes,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// NormalizedSpec represents the entire API contract.
type NormalizedSpec struct {
	Operations map[string]Operation `json:"operations"`
}

// ValidationError represents a mismatch between expected schema and actual value.
type ValidationError struct {
	Path     string
	Expected string
	Actual   string
	Message  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed at %q: expected %s, got %s (message: %s)", e.Path, e.Expected, e.Actual, e.Message)
}

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Match validates the given interface value against the schema.
func (s Schema) Match(val interface{}) error {
	return s.matchWithPath(val, "$")
}

func (s Schema) matchWithPath(val interface{}, path string) error {
	if val == nil {
		return &ValidationError{
			Path:     path,
			Expected: string(s.Type),
			Actual:   "nil",
			Message:  "value is nil",
		}
	}

	switch s.Type {
	case TypeScalar:
		return s.matchScalar(val, path)
	case TypeEnum:
		return s.matchEnum(val, path)
	case TypeArray:
		return s.matchArray(val, path)
	case TypeObject:
		return s.matchObject(val, path)
	default:
		return &ValidationError{
			Path:     path,
			Expected: "valid schema type",
			Actual:   string(s.Type),
			Message:  "unknown schema type",
		}
	}
}

func (s Schema) matchScalar(val interface{}, path string) error {
	switch s.ScalarType {
	case ScalarString:
		str, ok := val.(string)
		if !ok {
			return &ValidationError{
				Path:     path,
				Expected: "string",
				Actual:   fmt.Sprintf("%T", val),
				Message:  "type mismatch",
			}
		}
		return s.validateStringConstraints(str, path)

	case ScalarNumber, ScalarInteger:
		num, err := toFloat64(val)
		if err != nil {
			return &ValidationError{
				Path:     path,
				Expected: string(s.ScalarType),
				Actual:   fmt.Sprintf("%T", val),
				Message:  err.Error(),
			}
		}

		if s.ScalarType == ScalarInteger {
			if num != float64(int64(num)) {
				return &ValidationError{
					Path:     path,
					Expected: "integer",
					Actual:   fmt.Sprintf("%v", val),
					Message:  "value has fractional part",
				}
			}
		}
		return s.validateNumericConstraints(num, path)

	case ScalarBoolean:
		_, ok := val.(bool)
		if !ok {
			return &ValidationError{
				Path:     path,
				Expected: "boolean",
				Actual:   fmt.Sprintf("%T", val),
				Message:  "type mismatch",
			}
		}
		return nil

	default:
		return &ValidationError{
			Path:     path,
			Expected: "valid scalar type",
			Actual:   string(s.ScalarType),
			Message:  "unknown scalar type",
		}
	}
}

func (s Schema) validateStringConstraints(str string, path string) error {
	for _, c := range s.Constraints {
		switch c.Kind {
		case "pattern":
			re, err := regexp.Compile(c.Value)
			if err != nil {
				return &ValidationError{
					Path:     path,
					Expected: fmt.Sprintf("valid regex pattern %s", c.Value),
					Actual:   c.Value,
					Message:  fmt.Sprintf("invalid constraint regex: %v", err),
				}
			}
			if !re.MatchString(str) {
				return &ValidationError{
					Path:     path,
					Expected: fmt.Sprintf("match pattern %s", c.Value),
					Actual:   str,
					Message:  "pattern constraint violated",
				}
			}

		case "min-length":
			minLen, err := strconv.Atoi(c.Value)
			if err != nil {
				return &ValidationError{
					Path:     path,
					Expected: "integer min-length limit",
					Actual:   c.Value,
					Message:  fmt.Sprintf("invalid constraint limit: %v", err),
				}
			}
			if len(str) < minLen {
				return &ValidationError{
					Path:     path,
					Expected: fmt.Sprintf("length >= %d", minLen),
					Actual:   fmt.Sprintf("length %d", len(str)),
					Message:  "minimum length constraint violated",
				}
			}

		case "max-length":
			maxLen, err := strconv.Atoi(c.Value)
			if err != nil {
				return &ValidationError{
					Path:     path,
					Expected: "integer max-length limit",
					Actual:   c.Value,
					Message:  fmt.Sprintf("invalid constraint limit: %v", err),
				}
			}
			if len(str) > maxLen {
				return &ValidationError{
					Path:     path,
					Expected: fmt.Sprintf("length <= %d", maxLen),
					Actual:   fmt.Sprintf("length %d", len(str)),
					Message:  "maximum length constraint violated",
				}
			}

		case "format":
			switch c.Value {
			case "uuid":
				if !uuidRegex.MatchString(str) {
					return &ValidationError{
						Path:     path,
						Expected: "uuid format",
						Actual:   str,
						Message:  "uuid constraint violated",
					}
				}
			case "date-time":
				_, err := time.Parse(time.RFC3339, str)
				if err != nil {
					// Fallback to try parsing other common ISO-8601 layouts
					_, err = time.Parse("2006-01-02T15:04:05Z0700", str)
				}
				if err != nil {
					return &ValidationError{
						Path:     path,
						Expected: "RFC3339 date-time format",
						Actual:   str,
						Message:  fmt.Sprintf("date-time format constraint violated: %v", err),
					}
				}
			case "email":
				// Simple email validation regex
				emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
				if !emailRegex.MatchString(str) {
					return &ValidationError{
						Path:     path,
						Expected: "email format",
						Actual:   str,
						Message:  "email constraint violated",
					}
				}
			}
		}
	}
	return nil
}

func (s Schema) validateNumericConstraints(num float64, path string) error {
	for _, c := range s.Constraints {
		switch c.Kind {
		case "min":
			minVal, err := strconv.ParseFloat(c.Value, 64)
			if err != nil {
				return &ValidationError{
					Path:     path,
					Expected: "numeric min limit",
					Actual:   c.Value,
					Message:  fmt.Sprintf("invalid constraint minimum: %v", err),
				}
			}
			if num < minVal {
				return &ValidationError{
					Path:     path,
					Expected: fmt.Sprintf("value >= %v", minVal),
					Actual:   fmt.Sprintf("%v", num),
					Message:  "minimum value constraint violated",
				}
			}

		case "max":
			maxVal, err := strconv.ParseFloat(c.Value, 64)
			if err != nil {
				return &ValidationError{
					Path:     path,
					Expected: "numeric max limit",
					Actual:   c.Value,
					Message:  fmt.Sprintf("invalid constraint maximum: %v", err),
				}
			}
			if num > maxVal {
				return &ValidationError{
					Path:     path,
					Expected: fmt.Sprintf("value <= %v", maxVal),
					Actual:   fmt.Sprintf("%v", num),
					Message:  "maximum value constraint violated",
				}
			}
		}
	}
	return nil
}

func (s Schema) matchEnum(val interface{}, path string) error {
	str, ok := val.(string)
	if !ok {
		return &ValidationError{
			Path:     path,
			Expected: "string enum value",
			Actual:   fmt.Sprintf("%T", val),
			Message:  "type mismatch for enum",
		}
	}
	for _, expected := range s.EnumValues {
		if str == expected {
			return nil
		}
	}
	return &ValidationError{
		Path:     path,
		Expected: fmt.Sprintf("one of %v", s.EnumValues),
		Actual:   str,
		Message:  "enum constraint violated",
	}
}

func (s Schema) matchArray(val interface{}, path string) error {
	if s.Item == nil {
		return &ValidationError{
			Path:     path,
			Expected: "non-nil item schema",
			Actual:   "nil",
			Message:  "invalid array schema, missing item description",
		}
	}

	// Support slice or array types in Go using a reflection check
	switch v := val.(type) {
	case []interface{}:
		for i, item := range v {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			if err := s.Item.matchWithPath(item, itemPath); err != nil {
				return err
			}
		}
	case []string:
		for i, item := range v {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			if err := s.Item.matchWithPath(item, itemPath); err != nil {
				return err
			}
		}
	case []float64:
		for i, item := range v {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			if err := s.Item.matchWithPath(item, itemPath); err != nil {
				return err
			}
		}
	case []int:
		for i, item := range v {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			if err := s.Item.matchWithPath(item, itemPath); err != nil {
				return err
			}
		}
	default:
		// Try using standard reflection for other slices
		// This keeps validation robust if user passes custom slices
		return s.matchArrayReflection(val, path)
	}
	return nil
}

func (s Schema) matchArrayReflection(val interface{}, path string) error {
	v := reflect.ValueOf(val)
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return &ValidationError{
			Path:     path,
			Expected: "array or slice",
			Actual:   fmt.Sprintf("%T", val),
			Message:  "unsupported slice type or not a slice",
		}
	}

	for i := 0; i < v.Len(); i++ {
		item := v.Index(i).Interface()
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		if err := s.Item.matchWithPath(item, itemPath); err != nil {
			return err
		}
	}
	return nil
}

func (s Schema) matchObject(val interface{}, path string) error {
	objMap, ok := val.(map[string]interface{})
	if !ok {
		return &ValidationError{
			Path:     path,
			Expected: "object (map[string]interface{})",
			Actual:   fmt.Sprintf("%T", val),
			Message:  "type mismatch",
		}
	}

	// Verify required properties
	for _, reqKey := range s.Required {
		if _, exists := objMap[reqKey]; !exists {
			return &ValidationError{
				Path:     fmt.Sprintf("%s.%s", path, reqKey),
				Expected: "present",
				Actual:   "missing",
				Message:  "required property is missing",
			}
		}
	}

	// Recursively validate defined properties
	for key, subSchema := range s.Properties {
		subVal, exists := objMap[key]
		if exists {
			var propPath string
			if path == "$" {
				propPath = fmt.Sprintf("$.%s", key)
			} else {
				propPath = fmt.Sprintf("%s.%s", path, key)
			}
			if err := subSchema.matchWithPath(subVal, propPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func toFloat64(val interface{}) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", val)
	}
}
