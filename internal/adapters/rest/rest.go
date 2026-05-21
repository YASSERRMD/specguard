package rest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/YASSERRMD/specguard/internal/core"
	"github.com/getkin/kin-openapi/openapi3"
)

// Adapter implements core.ProtocolAdapter for REST/OpenAPI specifications.
type Adapter struct{}

// NewAdapter creates a new instance of the REST Adapter.
func NewAdapter() *Adapter {
	return &Adapter{}
}

// LoadSpec parses a raw OpenAPI specification (JSON or YAML) into the NormalizedSpec model.
func (a *Adapter) LoadSpec(source []byte) (spec *core.NormalizedSpec, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic recovered during openapi spec parsing: %v", r)
			spec = nil
		}
	}()

	loader := openapi3.NewLoader()
	doc, loadErr := loader.LoadFromData(source)
	if loadErr != nil {
		return nil, fmt.Errorf("failed to parse openapi document: %w", loadErr)
	}

	// Validate the specification document itself
	ctx := loader.Context
	loadErr = doc.Validate(ctx)
	if loadErr != nil {
		return nil, fmt.Errorf("invalid openapi specification: %w", loadErr)
	}

	operations := make(map[string]core.Operation)

	if doc.Paths != nil {
		for path, pathItem := range doc.Paths.Map() {
			if pathItem == nil {
				continue
			}
			for method, op := range pathItem.Operations() {
				if op == nil {
					continue
				}

				opID := op.OperationID
				if opID == "" {
					opID = strings.ToUpper(method) + "_" + path
				}

				normalizedOp, err := a.normalizeOperation(opID, path, method, op, pathItem.Parameters)
				if err != nil {
					return nil, fmt.Errorf("failed to normalize operation %s %s: %w", method, path, err)
				}
				operations[opID] = *normalizedOp
			}
		}
	}

	return &core.NormalizedSpec{
		Operations: operations,
	}, nil
}

func (a *Adapter) normalizeOperation(id, path, method string, op *openapi3.Operation, pathParams openapi3.Parameters) (*core.Operation, error) {
	allParams := make(openapi3.Parameters, 0, len(pathParams)+len(op.Parameters))
	allParams = append(allParams, pathParams...)
	allParams = append(allParams, op.Parameters...)

	inputSchema := core.Schema{
		Type:       core.TypeObject,
		Properties: make(map[string]core.Schema),
	}

	queryProps := make(map[string]core.Schema)
	pathProps := make(map[string]core.Schema)
	headerProps := make(map[string]core.Schema)
	var queryRequired, pathRequired, headerRequired []string

	for _, paramRef := range allParams {
		if paramRef == nil {
			continue
		}
		param := paramRef.Value
		if param == nil {
			continue
		}

		schema, err := a.translateSchema(param.Schema)
		if err != nil {
			return nil, fmt.Errorf("parameter %q schema translation failed: %w", param.Name, err)
		}

		switch param.In {
		case "query":
			queryProps[param.Name] = *schema
			if param.Required {
				queryRequired = append(queryRequired, param.Name)
			}
		case "path":
			pathProps[param.Name] = *schema
			if param.Required {
				pathRequired = append(pathRequired, param.Name)
			}
		case "header":
			headerProps[param.Name] = *schema
			if param.Required {
				headerRequired = append(headerRequired, param.Name)
			}
		}
	}

	if len(queryProps) > 0 {
		inputSchema.Properties["query"] = core.Schema{
			Type:       core.TypeObject,
			Properties: queryProps,
			Required:   queryRequired,
		}
	}
	if len(pathProps) > 0 {
		inputSchema.Properties["path"] = core.Schema{
			Type:       core.TypeObject,
			Properties: pathProps,
			Required:   pathRequired,
		}
	}
	if len(headerProps) > 0 {
		inputSchema.Properties["header"] = core.Schema{
			Type:       core.TypeObject,
			Properties: headerProps,
			Required:   headerRequired,
		}
	}

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		reqBody := op.RequestBody.Value
		if content, exists := reqBody.Content["application/json"]; exists && content.Schema != nil {
			schema, err := a.translateSchema(content.Schema)
			if err != nil {
				return nil, fmt.Errorf("request body schema translation failed: %w", err)
			}
			inputSchema.Properties["body"] = *schema
			if reqBody.Required {
				inputSchema.Required = append(inputSchema.Required, "body")
			}
		}
	}

	outputSchema := core.Schema{
		Type:       core.TypeObject,
		Properties: make(map[string]core.Schema),
	}
	errorShapes := make(map[string]core.Schema)

	if op.Responses != nil {
		for statusStr, respRef := range op.Responses.Map() {
			if respRef == nil {
				continue
			}
			resp := respRef.Value
			if resp == nil {
				continue
			}

			var schema *core.Schema
			if content, exists := resp.Content["application/json"]; exists && content.Schema != nil {
				var err error
				schema, err = a.translateSchema(content.Schema)
				if err != nil {
					return nil, fmt.Errorf("response %s schema translation failed: %w", statusStr, err)
				}
			} else {
				schema = &core.Schema{
					Type: core.TypeObject,
				}
			}

			isSuccess := false
			if strings.HasPrefix(statusStr, "2") {
				isSuccess = true
			}

			if isSuccess {
				outputSchema.Properties[statusStr] = *schema
			} else {
				errorShapes[statusStr] = *schema
			}
		}
	}

	metadata := map[string]string{
		"path":   path,
		"method": strings.ToUpper(method),
	}

	return &core.Operation{
		ID:          id,
		Input:       inputSchema,
		Output:      outputSchema,
		ErrorShapes: errorShapes,
		Metadata:    metadata,
	}, nil
}

func (a *Adapter) translateSchema(schemaRef *openapi3.SchemaRef) (*core.Schema, error) {
	if schemaRef == nil {
		return &core.Schema{Type: core.TypeObject}, nil
	}
	schema := schemaRef.Value
	if schema == nil {
		return &core.Schema{Type: core.TypeObject}, nil
	}

	var coreSchema core.Schema

	schemaType := ""
	if schema.Type != nil && len(schema.Type.Slice()) > 0 {
		schemaType = schema.Type.Slice()[0]
	}

	if len(schema.Enum) > 0 {
		coreSchema.Type = core.TypeEnum
		for _, val := range schema.Enum {
			coreSchema.EnumValues = append(coreSchema.EnumValues, fmt.Sprintf("%v", val))
		}
		return &coreSchema, nil
	}

	switch schemaType {
	case "object":
		coreSchema.Type = core.TypeObject
		coreSchema.Properties = make(map[string]core.Schema)
		coreSchema.Required = schema.Required

		for propName, propRef := range schema.Properties {
			propSchema, err := a.translateSchema(propRef)
			if err != nil {
				return nil, fmt.Errorf("property %q translation failed: %w", propName, err)
			}
			coreSchema.Properties[propName] = *propSchema
		}

	case "array":
		coreSchema.Type = core.TypeArray
		if schema.Items != nil {
			itemSchema, err := a.translateSchema(schema.Items)
			if err != nil {
				return nil, fmt.Errorf("array items translation failed: %w", err)
			}
			coreSchema.Item = itemSchema
		} else {
			coreSchema.Item = &core.Schema{Type: core.TypeObject}
		}

	case "string", "number", "integer", "boolean":
		coreSchema.Type = core.TypeScalar
		switch schemaType {
		case "string":
			coreSchema.ScalarType = core.ScalarString
			if schema.Pattern != "" {
				coreSchema.Constraints = append(coreSchema.Constraints, core.Constraint{
					Kind:  "pattern",
					Value: schema.Pattern,
				})
			}
			if schema.Format != "" {
				coreSchema.Constraints = append(coreSchema.Constraints, core.Constraint{
					Kind:  "format",
					Value: schema.Format,
				})
			}
			if schema.MinLength > 0 {
				coreSchema.Constraints = append(coreSchema.Constraints, core.Constraint{
					Kind:  "min-length",
					Value: strconv.FormatUint(schema.MinLength, 10),
				})
			}
			if schema.MaxLength != nil {
				coreSchema.Constraints = append(coreSchema.Constraints, core.Constraint{
					Kind:  "max-length",
					Value: strconv.FormatUint(*schema.MaxLength, 10),
				})
			}

		case "number", "integer":
			if schemaType == "number" {
				coreSchema.ScalarType = core.ScalarNumber
			} else {
				coreSchema.ScalarType = core.ScalarInteger
			}
			if schema.Min != nil {
				coreSchema.Constraints = append(coreSchema.Constraints, core.Constraint{
					Kind:  "min",
					Value: strconv.FormatFloat(*schema.Min, 'f', -1, 64),
				})
			}
			if schema.Max != nil {
				coreSchema.Constraints = append(coreSchema.Constraints, core.Constraint{
					Kind:  "max",
					Value: strconv.FormatFloat(*schema.Max, 'f', -1, 64),
				})
			}

		case "boolean":
			coreSchema.ScalarType = core.ScalarBoolean
		}

	default:
		if len(schema.Properties) > 0 {
			coreSchema.Type = core.TypeObject
			coreSchema.Properties = make(map[string]core.Schema)
			coreSchema.Required = schema.Required
			for propName, propRef := range schema.Properties {
				propSchema, err := a.translateSchema(propRef)
				if err != nil {
					return nil, fmt.Errorf("property %q translation failed: %w", propName, err)
				}
				coreSchema.Properties[propName] = *propSchema
			}
		} else if schema.Items != nil {
			coreSchema.Type = core.TypeArray
			itemSchema, err := a.translateSchema(schema.Items)
			if err != nil {
				return nil, fmt.Errorf("array items translation failed: %w", err)
			}
			coreSchema.Item = itemSchema
		} else {
			coreSchema.Type = core.TypeScalar
			coreSchema.ScalarType = core.ScalarString
		}
	}

	return &coreSchema, nil
}

// GenerateMock satisfies the core.ProtocolAdapter interface.
func (a *Adapter) GenerateMock(spec *core.NormalizedSpec, config core.MockConfig) (core.RunnableMock, error) {
	return NewMockServer(spec, config), nil
}

// RunContractChecks satisfies the core.ProtocolAdapter interface.
func (a *Adapter) RunContractChecks(spec *core.NormalizedSpec, targetURL string) (core.CheckResult, error) {
	report := &core.DriftReport{
		Findings: []core.Finding{},
	}

	client := &http.Client{Timeout: 10 * time.Second}

	for opID, op := range spec.Operations {
		method := op.Metadata["method"]
		pathPattern := op.Metadata["path"]
		if method == "" || pathPattern == "" {
			continue
		}

		actualPath := pathPattern
		if pathSchema, ok := op.Input.Properties["path"]; ok {
			pathVals := generateValueForSchema(pathSchema)
			if m, ok := pathVals.(map[string]interface{}); ok {
				for name, val := range m {
					actualPath = strings.Replace(actualPath, "{"+name+"}", fmt.Sprintf("%v", val), -1)
				}
			}
		}

		tURL := strings.TrimSuffix(targetURL, "/")
		if !strings.HasPrefix(actualPath, "/") {
			actualPath = "/" + actualPath
		}
		reqURL := tURL + actualPath

		if querySchema, ok := op.Input.Properties["query"]; ok {
			queryVals := generateValueForSchema(querySchema)
			if m, ok := queryVals.(map[string]interface{}); ok {
				queryParams := url.Values{}
				for name, val := range m {
					queryParams.Set(name, fmt.Sprintf("%v", val))
				}
				if len(queryParams) > 0 {
					reqURL += "?" + queryParams.Encode()
				}
			}
		}

		var bodyReader io.Reader
		var bodyBytes []byte
		if bodySchema, ok := op.Input.Properties["body"]; ok {
			bodyVal := generateValueForSchema(bodySchema)
			var err error
			bodyBytes, err = json.Marshal(bodyVal)
			if err != nil {
				return core.CheckResult{}, fmt.Errorf("failed to marshal generated body for op %s: %w", opID, err)
			}
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequest(method, reqURL, bodyReader)
		if err != nil {
			return core.CheckResult{}, fmt.Errorf("failed to create http request for op %s: %w", opID, err)
		}

		if headerSchema, ok := op.Input.Properties["header"]; ok {
			headerVals := generateValueForSchema(headerSchema)
			if m, ok := headerVals.(map[string]interface{}); ok {
				for name, val := range m {
					req.Header.Set(name, fmt.Sprintf("%v", val))
				}
			}
		}

		if bodyReader != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := client.Do(req)
		if err != nil {
			report.Findings = append(report.Findings, core.Finding{
				Location: "operations." + opID,
				Kind:     core.KindMissing,
				Expected: "reachable target",
				Actual:   "error: " + err.Error(),
				Severity: core.SeverityError,
			})
			continue
		}

		respBodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			report.Findings = append(report.Findings, core.Finding{
				Location: "operations." + opID,
				Kind:     core.KindMissing,
				Expected: "readable response body",
				Actual:   "error: " + err.Error(),
				Severity: core.SeverityError,
			})
			continue
		}

		statusStr := strconv.Itoa(resp.StatusCode)

		var matchSchema *core.Schema
		var isError bool

		if schema, exists := op.Output.Properties[statusStr]; exists {
			matchSchema = &schema
			isError = false
		} else if schema, exists := op.ErrorShapes[statusStr]; exists {
			matchSchema = &schema
			isError = true
		}

		if matchSchema == nil {
			report.Findings = append(report.Findings, core.Finding{
				Location: "operations." + opID + ".output." + statusStr,
				Kind:     core.KindMissing,
				Expected: "response status code defined in spec",
				Actual:   "status " + statusStr,
				Severity: core.SeverityError,
			})
			continue
		}

		var parsedBody interface{}
		if len(respBodyBytes) > 0 {
			if err := json.Unmarshal(respBodyBytes, &parsedBody); err != nil {
				report.Findings = append(report.Findings, core.Finding{
					Location: "operations." + opID + ".output." + statusStr,
					Kind:     core.KindTypeChanged,
					Expected: "application/json",
					Actual:   "malformed JSON: " + err.Error(),
					Severity: core.SeverityError,
				})
				continue
			}
		}

		if err := matchSchema.Match(parsedBody); err != nil {
			severity := core.SeverityError
			if isError {
				severity = core.SeverityWarning
			}
			report.Findings = append(report.Findings, core.Finding{
				Location: "operations." + opID + ".output." + statusStr,
				Kind:     core.KindConstraintViolated,
				Expected: "conformant schema structure",
				Actual:   err.Error(),
				Severity: severity,
			})
		}
	}

	passed := len(report.Findings) == 0
	return core.CheckResult{
		Passed:      passed,
		DriftReport: report,
	}, nil
}

func generateValueForSchema(s core.Schema) interface{} {
	switch s.Type {
	case core.TypeScalar:
		switch s.ScalarType {
		case core.ScalarInteger:
			val := 1
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if v, err := strconv.Atoi(c.Value); err == nil && val < v {
						val = v
					}
				}
			}
			return val
		case core.ScalarNumber:
			val := 1.0
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if v, err := strconv.ParseFloat(c.Value, 64); err == nil && val < v {
						val = v
					}
				}
			}
			return val
		case core.ScalarBoolean:
			return true
		case core.ScalarString:
			for _, c := range s.Constraints {
				if c.Kind == "format" {
					if c.Value == "uuid" {
						return "123e4567-e89b-12d3-a456-426614174000"
					}
					if c.Value == "date-time" {
						return "2026-05-21T06:10:00Z"
					}
				}
			}
			return "mock_value"
		default:
			return "mock_value"
		}
	case core.TypeEnum:
		if len(s.EnumValues) > 0 {
			return s.EnumValues[0]
		}
		return "enum_default"
	case core.TypeArray:
		if s.Item != nil {
			return []interface{}{generateValueForSchema(*s.Item)}
		}
		return []interface{}{}
	case core.TypeObject:
		res := make(map[string]interface{})
		for propName, propSchema := range s.Properties {
			res[propName] = generateValueForSchema(propSchema)
		}
		return res
	default:
		return nil
	}
}

// NormalizeResult satisfies the core.ProtocolAdapter interface.
func (a *Adapter) NormalizeResult(rawResult interface{}) (*core.DriftReport, error) {
	switch v := rawResult.(type) {
	case *core.DriftReport:
		return v, nil
	case core.DriftReport:
		return &v, nil
	case []byte:
		var r core.DriftReport
		if err := json.Unmarshal(v, &r); err != nil {
			return nil, err
		}
		return &r, nil
	case string:
		var r core.DriftReport
		if err := json.Unmarshal([]byte(v), &r); err != nil {
			return nil, err
		}
		return &r, nil
	default:
		return nil, fmt.Errorf("unsupported raw result type: %T", rawResult)
	}
}
