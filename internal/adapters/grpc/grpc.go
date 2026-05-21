package grpc

import (
	"context"
	"fmt"

	"github.com/YASSERRMD/specguard/internal/core"
	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Adapter implements core.ProtocolAdapter for gRPC/Protobuf specifications.
type Adapter struct{}

// NewAdapter creates a new instance of the gRPC Adapter.
func NewAdapter() *Adapter {
	return &Adapter{}
}

// LoadSpec parses a raw protobuf specification (.proto) into the NormalizedSpec model.
func (a *Adapter) LoadSpec(source []byte) (*core.NormalizedSpec, error) {
	files := map[string]string{
		"input.proto": string(source),
	}
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(files),
		}),
	}

	fds, err := compiler.Compile(context.Background(), "input.proto")
	if err != nil {
		return nil, fmt.Errorf("failed to compile proto: %w", err)
	}

	if len(fds) == 0 {
		return nil, fmt.Errorf("no compiled descriptors found")
	}

	fd := fds[0]
	operations := make(map[string]core.Operation)

	services := fd.Services()
	for i := 0; i < services.Len(); i++ {
		service := services.Get(i)
		methods := service.Methods()
		for j := 0; j < methods.Len(); j++ {
			method := methods.Get(j)
			op, err := a.normalizeMethod(method, string(source))
			if err != nil {
				return nil, fmt.Errorf("failed to normalize method %s: %w", method.FullName(), err)
			}
			operations[op.ID] = *op
		}
	}

	return &core.NormalizedSpec{
		Operations: operations,
	}, nil
}

func (a *Adapter) normalizeMethod(method protoreflect.MethodDescriptor, protoSource string) (*core.Operation, error) {
	// Standard gRPC path format: /Package.Service/Method
	path := fmt.Sprintf("/%s/%s", method.Parent().FullName(), method.Name())

	visitedInput := make(map[protoreflect.FullName]bool)
	inputSchema, err := a.translateMessage(method.Input(), visitedInput)
	if err != nil {
		return nil, fmt.Errorf("failed to translate input message %s: %w", method.Input().FullName(), err)
	}

	visitedOutput := make(map[protoreflect.FullName]bool)
	outputMsgSchema, err := a.translateMessage(method.Output(), visitedOutput)
	if err != nil {
		return nil, fmt.Errorf("failed to translate output message %s: %w", method.Output().FullName(), err)
	}

	// gRPC output maps success response to "0" (status code OK)
	outputSchema := core.Schema{
		Type: core.TypeObject,
		Properties: map[string]core.Schema{
			"0": *outputMsgSchema,
		},
	}

	metadata := map[string]string{
		"protocol":     "grpc",
		"path":         path,
		"service":      string(method.Parent().FullName()),
		"method":       string(method.Name()),
		"proto_source": protoSource,
	}

	return &core.Operation{
		ID:          path,
		Input:       *inputSchema,
		Output:      outputSchema,
		ErrorShapes: make(map[string]core.Schema),
		Metadata:    metadata,
	}, nil
}

func (a *Adapter) translateMessage(desc protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool) (*core.Schema, error) {
	if visited[desc.FullName()] {
		return &core.Schema{Type: core.TypeObject}, nil
	}
	visited[desc.FullName()] = true
	defer func() { visited[desc.FullName()] = false }()

	properties := make(map[string]core.Schema)
	var required []string

	fields := desc.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		fieldName := string(field.Name())
		fieldSchema, err := a.translateField(field, visited)
		if err != nil {
			return nil, err
		}
		properties[fieldName] = *fieldSchema
	}

	return &core.Schema{
		Type:       core.TypeObject,
		Properties: properties,
		Required:   required,
	}, nil
}

func (a *Adapter) translateField(field protoreflect.FieldDescriptor, visited map[protoreflect.FullName]bool) (*core.Schema, error) {
	var schema core.Schema
	if field.IsList() {
		schema.Type = core.TypeArray
		itemSchema, err := a.translateFieldBase(field, visited)
		if err != nil {
			return nil, err
		}
		schema.Item = itemSchema
		return &schema, nil
	}
	if field.IsMap() {
		schema.Type = core.TypeObject
		valSchema, err := a.translateFieldBase(field.MapValue(), visited)
		if err != nil {
			return nil, err
		}
		schema.Properties = map[string]core.Schema{
			"*": *valSchema,
		}
		return &schema, nil
	}
	return a.translateFieldBase(field, visited)
}

func (a *Adapter) translateFieldBase(field protoreflect.FieldDescriptor, visited map[protoreflect.FullName]bool) (*core.Schema, error) {
	var schema core.Schema
	switch field.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return a.translateMessage(field.Message(), visited)
	case protoreflect.EnumKind:
		schema.Type = core.TypeEnum
		enumValues := field.Enum().Values()
		for i := 0; i < enumValues.Len(); i++ {
			schema.EnumValues = append(schema.EnumValues, string(enumValues.Get(i).Name()))
		}
		return &schema, nil
	case protoreflect.StringKind:
		schema.Type = core.TypeScalar
		schema.ScalarType = core.ScalarString
	case protoreflect.BytesKind:
		schema.Type = core.TypeScalar
		schema.ScalarType = core.ScalarString
	case protoreflect.BoolKind:
		schema.Type = core.TypeScalar
		schema.ScalarType = core.ScalarBoolean
	case protoreflect.DoubleKind, protoreflect.FloatKind:
		schema.Type = core.TypeScalar
		schema.ScalarType = core.ScalarNumber
	default:
		schema.Type = core.TypeScalar
		schema.ScalarType = core.ScalarInteger
	}
	return &schema, nil
}

// GenerateMock satisfies core.ProtocolAdapter.
func (a *Adapter) GenerateMock(spec *core.NormalizedSpec, config core.MockConfig) (core.RunnableMock, error) {
	return NewMockServer(spec, config), nil
}

// RunContractChecks satisfies core.ProtocolAdapter.
func (a *Adapter) RunContractChecks(spec *core.NormalizedSpec, targetURL string) (core.CheckResult, error) {
	return core.CheckResult{}, fmt.Errorf("gRPC contract checks not implemented yet")
}

// NormalizeResult satisfies core.ProtocolAdapter.
func (a *Adapter) NormalizeResult(rawResult interface{}) (*core.DriftReport, error) {
	return nil, fmt.Errorf("gRPC normalize result not implemented yet")
}
