package rest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

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
func (a *Adapter) LoadSpec(source []byte) (*core.NormalizedSpec, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(source)
	if err != nil {
		return nil, fmt.Errorf("failed to parse openapi document: %w", err)
	}

	// Validate the specification document itself
	ctx := loader.Context
	err = doc.Validate(ctx)
	if err != nil {
		return nil, fmt.Errorf("invalid openapi specification: %w", err)
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
	return core.CheckResult{}, errors.New("contract checks not implemented for REST adapter")
}

// NormalizeResult satisfies the core.ProtocolAdapter interface.
func (a *Adapter) NormalizeResult(rawResult interface{}) (*core.DriftReport, error) {
	return nil, errors.New("normalize result not implemented for REST adapter")
}
