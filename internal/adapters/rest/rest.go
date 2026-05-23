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

	if schema.Nullable {
		coreSchema.Nullable = true
	}
	if schema.Default != nil {
		coreSchema.Default = schema.Default
	}
	if schema.Example != nil {
		coreSchema.Example = schema.Example
	}

	if len(schema.OneOf) > 0 {
		coreSchema.OneOf = make([]core.Schema, 0, len(schema.OneOf))
		for _, subRef := range schema.OneOf {
			subSchema, err := a.translateSchema(subRef)
			if err != nil {
				return nil, err
			}
			coreSchema.OneOf = append(coreSchema.OneOf, *subSchema)
		}
	}

	if len(schema.AnyOf) > 0 {
		coreSchema.AnyOf = make([]core.Schema, 0, len(schema.AnyOf))
		for _, subRef := range schema.AnyOf {
			subSchema, err := a.translateSchema(subRef)
			if err != nil {
				return nil, err
			}
			coreSchema.AnyOf = append(coreSchema.AnyOf, *subSchema)
		}
	}

	if len(schema.AllOf) > 0 {
		coreSchema.AllOf = make([]core.Schema, 0, len(schema.AllOf))
		for _, subRef := range schema.AllOf {
			subSchema, err := a.translateSchema(subRef)
			if err != nil {
				return nil, err
			}
			coreSchema.AllOf = append(coreSchema.AllOf, *subSchema)
		}
	}

	if schema.AdditionalProperties.Schema != nil {
		additionalSchema, err := a.translateSchema(schema.AdditionalProperties.Schema)
		if err != nil {
			return nil, err
		}
		coreSchema.AdditionalProperties = additionalSchema
	}

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

		type restVariant struct {
			name        string
			expectError bool
			pathVals    interface{}
			queryVals   interface{}
			bodyVal     interface{}
			headerVals  interface{}
		}

		var variants []restVariant

		// Extract schemas
		pathSchema, hasPath := op.Input.Properties["path"]
		querySchema, hasQuery := op.Input.Properties["query"]
		bodySchema, hasBody := op.Input.Properties["body"]
		headerSchema, _ := op.Input.Properties["header"]

		// 1. Base Happy Path
		basePath := core.GenerateValueForSchema(pathSchema)
		baseQuery := core.GenerateValueForSchema(querySchema)
		baseBody := core.GenerateValueForSchema(bodySchema)
		baseHeader := core.GenerateValueForSchema(headerSchema)
		variants = append(variants, restVariant{
			name:        "base_happy_path",
			expectError: false,
			pathVals:    basePath,
			queryVals:   baseQuery,
			bodyVal:     baseBody,
			headerVals:  baseHeader,
		})

		// 2. Edge Case
		variants = append(variants, restVariant{
			name:        "edge_case_request",
			expectError: false,
			pathVals:    core.GenerateEdgeCaseValueForSchema(pathSchema),
			queryVals:   core.GenerateEdgeCaseValueForSchema(querySchema),
			bodyVal:     core.GenerateEdgeCaseValueForSchema(bodySchema),
			headerVals:  core.GenerateEdgeCaseValueForSchema(headerSchema),
		})

		// Only test invalid requests if the spec documents error responses
		if len(op.ErrorShapes) > 0 {
			// 3. Invalid Body (if body schema exists)
			if hasBody && bodySchema.Type != "" {
				variants = append(variants, restVariant{
					name:        "invalid_body_request",
					expectError: true,
					pathVals:    basePath,
					queryVals:   baseQuery,
					bodyVal:     core.GenerateInvalidValueForSchema(bodySchema),
					headerVals:  baseHeader,
				})
			}

			// 4. Invalid Query (if query schema exists)
			if hasQuery && querySchema.Type != "" {
				variants = append(variants, restVariant{
					name:        "invalid_query_request",
					expectError: true,
					pathVals:    basePath,
					queryVals:   core.GenerateInvalidValueForSchema(querySchema),
					bodyVal:     baseBody,
					headerVals:  baseHeader,
				})
			}

			// 5. Invalid Path (if path schema exists)
			if hasPath && pathSchema.Type != "" {
				variants = append(variants, restVariant{
					name:        "invalid_path_request",
					expectError: true,
					pathVals:    core.GenerateInvalidValueForSchema(pathSchema),
					queryVals:   baseQuery,
					bodyVal:     baseBody,
					headerVals:  baseHeader,
				})
			}
		}

		for _, v := range variants {
			actualPath := pathPattern
			if v.pathVals != nil {
				if m, ok := v.pathVals.(map[string]interface{}); ok {
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

			if v.queryVals != nil {
				if m, ok := v.queryVals.(map[string]interface{}); ok {
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
			if v.bodyVal != nil {
				var err error
				bodyBytes, err = json.Marshal(v.bodyVal)
				if err != nil {
					return core.CheckResult{}, fmt.Errorf("failed to marshal generated body for op %s (%s): %w", opID, v.name, err)
				}
				bodyReader = bytes.NewReader(bodyBytes)
			}

			req, err := http.NewRequest(method, reqURL, bodyReader)
			if err != nil {
				return core.CheckResult{}, fmt.Errorf("failed to create http request for op %s (%s): %w", opID, v.name, err)
			}

			if v.headerVals != nil {
				if m, ok := v.headerVals.(map[string]interface{}); ok {
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
					Actual:   fmt.Sprintf("error: %s (variant: %s)", err.Error(), v.name),
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
					Actual:   fmt.Sprintf("error: %s (variant: %s)", err.Error(), v.name),
					Severity: core.SeverityError,
				})
				continue
			}

			statusStr := strconv.Itoa(resp.StatusCode)

			// If we expect an error response (e.g. 4xx range)
			if v.expectError {
				if !strings.HasPrefix(statusStr, "4") && !strings.HasPrefix(statusStr, "5") {
					report.Findings = append(report.Findings, core.Finding{
						Location: "operations." + opID + ".input",
						Kind:     core.KindConstraintViolated,
						Expected: "error status code (4xx or 5xx)",
						Actual:   fmt.Sprintf("%s (variant: %s)", statusStr, v.name),
						Severity: core.SeverityError,
					})
				}
				// Also validate error shape if defined in spec
				if schema, exists := op.ErrorShapes[statusStr]; exists {
					var parsedBody interface{}
					if len(respBodyBytes) > 0 {
						if err := json.Unmarshal(respBodyBytes, &parsedBody); err == nil {
							if err := schema.Match(parsedBody); err != nil {
								report.Findings = append(report.Findings, core.Finding{
									Location: "operations." + opID + ".error_shapes." + statusStr,
									Kind:     core.KindConstraintViolated,
									Expected: "conformant error schema",
									Actual:   fmt.Sprintf("%s (variant: %s)", err.Error(), v.name),
									Severity: core.SeverityWarning,
								})
							}
						}
					}
				}
				continue
			}

			// If we expect success (2xx) but get error or alternate status
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
					Actual:   fmt.Sprintf("status %s (variant: %s)", statusStr, v.name),
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
						Actual:   fmt.Sprintf("malformed JSON: %s (variant: %s)", err.Error(), v.name),
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
					Actual:   fmt.Sprintf("%s (variant: %s)", err.Error(), v.name),
					Severity: severity,
				})
			}
		}
	}

	passed := len(report.Findings) == 0
	return core.CheckResult{
		Passed:      passed,
		DriftReport: report,
	}, nil
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
