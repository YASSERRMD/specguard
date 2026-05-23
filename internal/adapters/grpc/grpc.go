package grpc

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/YASSERRMD/specguard/internal/core"
	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Adapter implements core.ProtocolAdapter for gRPC/Protobuf specifications.
type Adapter struct{}

// NewAdapter creates a new instance of the gRPC Adapter.
func NewAdapter() *Adapter {
	return &Adapter{}
}

// LoadSpec parses a raw protobuf specification (.proto) into the NormalizedSpec model.
func (a *Adapter) LoadSpec(source []byte) (spec *core.NormalizedSpec, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic recovered during protobuf spec loading: %v", r)
		}
	}()

	if len(source) == 0 {
		return nil, fmt.Errorf("proto source is empty")
	}

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
		if field.Cardinality() == protoreflect.Required {
			required = append(required, fieldName)
		}
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
	report := &core.DriftReport{
		Findings: []core.Finding{},
	}

	// 1. Compile spec proto source to build methods map.
	var protoSource string
	for _, op := range spec.Operations {
		if src, exists := op.Metadata["proto_source"]; exists && src != "" {
			protoSource = src
			break
		}
	}
	if protoSource == "" {
		return core.CheckResult{}, fmt.Errorf("missing proto_source metadata in spec")
	}

	files := map[string]string{
		"input.proto": protoSource,
	}
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(files),
		}),
	}
	fds, err := compiler.Compile(context.Background(), "input.proto")
	if err != nil {
		return core.CheckResult{}, fmt.Errorf("failed to compile proto: %w", err)
	}
	if len(fds) == 0 {
		return core.CheckResult{}, fmt.Errorf("no compiled descriptors found")
	}

	fd := fds[0]
	methods := make(map[string]protoreflect.MethodDescriptor)
	services := fd.Services()
	for i := 0; i < services.Len(); i++ {
		service := services.Get(i)
		methodList := service.Methods()
		for j := 0; j < methodList.Len(); j++ {
			method := methodList.Get(j)
			path := fmt.Sprintf("/%s/%s", service.FullName(), method.Name())
			methods[path] = method
		}
	}

	// 2. Establish connection to SUT.
	useTLS := false
	address := targetURL
	if strings.HasPrefix(targetURL, "grpcs://") {
		useTLS = true
		address = strings.TrimPrefix(targetURL, "grpcs://")
	} else if strings.HasPrefix(targetURL, "grpc://") {
		useTLS = false
		address = strings.TrimPrefix(targetURL, "grpc://")
	} else {
		if strings.HasSuffix(targetURL, ":443") || strings.Contains(targetURL, ":443/") {
			useTLS = true
		}
	}

	var creds credentials.TransportCredentials
	if useTLS {
		creds = credentials.NewTLS(&tls.Config{})
	} else {
		creds = insecure.NewCredentials()
	}

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(creds))
	if err != nil {
		return core.CheckResult{}, fmt.Errorf("failed to dial target %s: %w", targetURL, err)
	}
	defer conn.Close()

	// 3. For each operation in spec
	for opID, op := range spec.Operations {
		// Skip operations that are not grpc protocol
		if proto, ok := op.Metadata["protocol"]; !ok || proto != "grpc" {
			continue
		}

		methodDesc, ok := methods[op.ID]
		if !ok {
			report.Findings = append(report.Findings, core.Finding{
				Location: "operations." + opID,
				Kind:     core.KindMissing,
				Expected: "method descriptor in compiled proto",
				Actual:   "not found",
				Severity: core.SeverityError,
			})
			continue
		}

		reqVal := core.GenerateValueForSchema(op.Input)
		reqJSON, err := json.Marshal(reqVal)
		if err != nil {
			return core.CheckResult{}, fmt.Errorf("failed to marshal request value for %s: %w", opID, err)
		}

		inMsg := dynamicpb.NewMessage(methodDesc.Input())
		uOpts := protojson.UnmarshalOptions{DiscardUnknown: true}
		if err := uOpts.Unmarshal(reqJSON, inMsg); err != nil {
			return core.CheckResult{}, fmt.Errorf("failed to unmarshal request JSON to protobuf for %s: %w", opID, err)
		}

		isClientStream := methodDesc.IsStreamingClient()
		isServerStream := methodDesc.IsStreamingServer()

		var callErr error
		var responses []*dynamicpb.Message

		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if !isClientStream && !isServerStream {
				// Unary
				outMsg := dynamicpb.NewMessage(methodDesc.Output())
				callErr = conn.Invoke(ctx, op.ID, inMsg, outMsg)
				if callErr == nil {
					responses = append(responses, outMsg)
				}
			} else {
				desc := &grpc.StreamDesc{
					StreamName:    string(methodDesc.Name()),
					ServerStreams: isServerStream,
					ClientStreams: isClientStream,
				}
				str, streamErr := conn.NewStream(ctx, desc, op.ID)
				if streamErr != nil {
					callErr = streamErr
					return
				}

				if isClientStream && !isServerStream {
					// Client Streaming
					if err := str.SendMsg(inMsg); err != nil {
						callErr = err
					} else if err := str.SendMsg(inMsg); err != nil {
						callErr = err
					} else if err := str.CloseSend(); err != nil {
						callErr = err
					} else {
						outMsg := dynamicpb.NewMessage(methodDesc.Output())
						if err := str.RecvMsg(outMsg); err != nil {
							callErr = err
						} else {
							responses = append(responses, outMsg)
						}
					}
				} else if !isClientStream && isServerStream {
					// Server Streaming
					if err := str.SendMsg(inMsg); err != nil {
						callErr = err
					} else if err := str.CloseSend(); err != nil {
						callErr = err
					} else {
						for {
							outMsg := dynamicpb.NewMessage(methodDesc.Output())
							if err := str.RecvMsg(outMsg); err == io.EOF {
								break
							} else if err != nil {
								callErr = err
								break
							} else {
								responses = append(responses, outMsg)
							}
						}
					}
				} else {
					// Bidirectional Streaming
					if err := str.SendMsg(inMsg); err != nil {
						callErr = err
					} else {
						outMsg1 := dynamicpb.NewMessage(methodDesc.Output())
						if err := str.RecvMsg(outMsg1); err != nil {
							callErr = err
						} else {
							responses = append(responses, outMsg1)
							if err := str.SendMsg(inMsg); err != nil {
								callErr = err
							} else {
								outMsg2 := dynamicpb.NewMessage(methodDesc.Output())
								if err := str.RecvMsg(outMsg2); err != nil {
									callErr = err
								} else {
									responses = append(responses, outMsg2)
								}
							}
						}
					}
					if callErr == nil {
						if err := str.CloseSend(); err != nil {
							callErr = err
						} else {
							for {
								outMsg := dynamicpb.NewMessage(methodDesc.Output())
								if err := str.RecvMsg(outMsg); err == io.EOF {
									break
								} else if err != nil {
									callErr = err
									break
								} else {
									responses = append(responses, outMsg)
								}
							}
						}
					}
				}
			}
		}()

		if callErr != nil {
			st, ok := status.FromError(callErr)
			if ok {
				statusStr := strconv.Itoa(int(st.Code()))
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
						Expected: "status code defined in spec",
						Actual:   "status " + statusStr + " (" + st.Code().String() + ")",
						Severity: core.SeverityError,
					})
				} else {
					details := st.Details()
					var parsedDetail interface{}
					if len(details) > 0 {
						if pm, ok := details[0].(protoreflect.ProtoMessage); ok {
							mOpts := protojson.MarshalOptions{UseProtoNames: true}
							jsonBytes, err := mOpts.Marshal(pm)
							if err == nil {
								_ = json.Unmarshal(jsonBytes, &parsedDetail)
							}
						} else {
							parsedDetail = fmt.Sprintf("%v", details[0])
						}
					} else {
						parsedDetail = map[string]interface{}{
							"error": st.Message(),
						}
					}

					if err := matchSchema.Match(parsedDetail); err != nil {
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
			} else {
				report.Findings = append(report.Findings, core.Finding{
					Location: "operations." + opID,
					Kind:     core.KindMissing,
					Expected: "gRPC status code response",
					Actual:   "error: " + callErr.Error(),
					Severity: core.SeverityError,
				})
			}
			continue
		}

		// Validate all successful responses
		for _, resp := range responses {
			mOpts := protojson.MarshalOptions{UseProtoNames: true}
			jsonBytes, err := mOpts.Marshal(resp)
			if err != nil {
				report.Findings = append(report.Findings, core.Finding{
					Location: "operations." + opID + ".output.0",
					Kind:     core.KindTypeChanged,
					Expected: "valid protobuf marshal",
					Actual:   err.Error(),
					Severity: core.SeverityError,
				})
				continue
			}

			var respVal interface{}
			if err := json.Unmarshal(jsonBytes, &respVal); err != nil {
				report.Findings = append(report.Findings, core.Finding{
					Location: "operations." + opID + ".output.0",
					Kind:     core.KindTypeChanged,
					Expected: "valid JSON unmarshal",
					Actual:   err.Error(),
					Severity: core.SeverityError,
				})
				continue
			}

			successSchema, hasSuccess := op.Output.Properties["0"]
			if !hasSuccess {
				report.Findings = append(report.Findings, core.Finding{
					Location: "operations." + opID + ".output.0",
					Kind:     core.KindMissing,
					Expected: "success schema '0' in operation output properties",
					Actual:   "missing",
					Severity: core.SeverityError,
				})
				continue
			}

			if err := successSchema.Match(respVal); err != nil {
				kind := core.KindConstraintViolated
				expectedVal := "conformant schema structure"
				actualVal := err.Error()
				location := "operations." + opID + ".output.0"

				if vErr, ok := err.(*core.ValidationError); ok {
					expectedVal = vErr.Expected
					actualVal = vErr.Actual
					if vErr.Path != "" && vErr.Path != "$" {
						location = "operations." + opID + ".output.0." + strings.TrimPrefix(vErr.Path, "$.")
					}
					if vErr.Actual == "missing" {
						kind = core.KindMissing
					} else {
						kind = core.KindTypeChanged
					}
				}

				report.Findings = append(report.Findings, core.Finding{
					Location: location,
					Kind:     kind,
					Expected: expectedVal,
					Actual:   actualVal,
					Severity: core.SeverityError,
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

// NormalizeResult satisfies core.ProtocolAdapter.
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
