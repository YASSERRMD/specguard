package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/YASSERRMD/specguard/internal/core"
	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// MockServer implements core.RunnableMock for gRPC mock servers.
type MockServer struct {
	spec     *core.NormalizedSpec
	config   core.MockConfig
	listener net.Listener
	server   *grpc.Server
	address  string
	mu       sync.Mutex
	running  bool
	methods  map[string]protoreflect.MethodDescriptor
}

// NewMockServer creates a new instance of MockServer.
func NewMockServer(spec *core.NormalizedSpec, config core.MockConfig) *MockServer {
	return &MockServer{
		spec:    spec,
		config:  config,
		methods: make(map[string]protoreflect.MethodDescriptor),
	}
}

// Start launches the gRPC mock server.
func (m *MockServer) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("mock server is already running")
	}

	// 1. Find proto source from spec
	var protoSource string
	for _, op := range m.spec.Operations {
		if src, exists := op.Metadata["proto_source"]; exists && src != "" {
			protoSource = src
			break
		}
	}

	if protoSource == "" {
		return fmt.Errorf("missing proto_source metadata in specification")
	}

	// 2. Compile proto in-memory
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
		return fmt.Errorf("failed to compile proto: %w", err)
	}

	if len(fds) == 0 {
		return fmt.Errorf("no compiled descriptors found")
	}

	fd := fds[0]
	services := fd.Services()
	for i := 0; i < services.Len(); i++ {
		service := services.Get(i)
		methods := service.Methods()
		for j := 0; j < methods.Len(); j++ {
			method := methods.Get(j)
			path := fmt.Sprintf("/%s/%s", service.FullName(), method.Name())
			m.methods[path] = method
		}
	}

	// 3. Listen on port
	addrStr := fmt.Sprintf("%s:%d", m.config.Host, m.config.Port)
	lis, err := net.Listen("tcp", addrStr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addrStr, err)
	}

	m.listener = lis
	m.address = lis.Addr().String()

	// 4. Create grpc server with unknown service handler
	m.server = grpc.NewServer(
		grpc.UnknownServiceHandler(m.handleStream),
	)

	m.running = true

	go func() {
		_ = m.server.Serve(lis)
	}()

	return nil
}

// Stop gracefully shuts down the server.
func (m *MockServer) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	m.server.GracefulStop()
	_ = m.listener.Close()
	m.running = false
	return nil
}

// GetAddress returns the listening URL or address of the mock server.
func (m *MockServer) GetAddress() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.address
}

func (m *MockServer) processMessageAndValidate(ctx context.Context, op core.Operation, methodDesc protoreflect.MethodDescriptor, inMsg *dynamicpb.Message, stream grpc.ServerStream) (*dynamicpb.Message, error) {
	// Convert incoming msg to standard map[string]interface{} using JSON translation
	mOpts := protojson.MarshalOptions{UseProtoNames: true}
	jsonBytes, err := mOpts.Marshal(inMsg)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal request message: %v", err)
	}

	var val interface{}
	if err := json.Unmarshal(jsonBytes, &val); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to parse request JSON: %v", err)
	}

	// Validate against schema
	if err := op.Input.Match(val); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "validation failed: %v", err)
	}

	// Retrieve scenario selection
	scenarioName := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-mock-scenario"); len(vals) > 0 {
			scenarioName = vals[0]
		} else if vals := md.Get("_scenario"); len(vals) > 0 {
			scenarioName = vals[0]
		} else if vals := md.Get("x-scenario"); len(vals) > 0 {
			scenarioName = vals[0]
		} else if vals := md.Get("scenario"); len(vals) > 0 {
			scenarioName = vals[0]
		}
	}

	var respStatus int
	var respBody interface{}
	var respHeaders map[string]interface{}
	hasScenario := false

	if scenarioName != "" && scenarioName != "success" {
		respStatus, respBody, respHeaders, hasScenario = m.getScenarioResponse(op, scenarioName)
	}

	// Send metadata/headers back if defined
	if len(respHeaders) > 0 {
		md := metadata.New(nil)
		for k, v := range respHeaders {
			md.Set(k, fmt.Sprintf("%v", v))
		}
		if err := stream.SendHeader(md); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to send metadata headers: %v", err)
		}
	}

	if hasScenario {
		if respStatus != 0 {
			gCode := m.mapHTTPStatusToGRPC(respStatus)
			errMsg := fmt.Sprintf("Simulated scenario %s", scenarioName)
			if mBody, ok := respBody.(map[string]interface{}); ok {
				if msg, ok := mBody["error"].(string); ok {
					errMsg = msg
				}
			} else if sMsg, ok := respBody.(string); ok {
				errMsg = sMsg
			}
			return nil, status.Error(gCode, errMsg)
		}
	}

	// Generate mock output values
	var mockVal interface{}
	if hasScenario && respBody != nil {
		mockVal = respBody
	} else {
		successSchema, ok := op.Output.Properties["0"]
		if !ok {
			return nil, status.Errorf(codes.Internal, "missing success schema for method %s", methodDesc.FullName())
		}
		mockVal = generateMockValue(successSchema)
	}

	// Serialize mock values to JSON and load into dynamic pb message
	respJSON, err := json.Marshal(mockVal)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal mock response: %v", err)
	}

	outDesc := methodDesc.Output()
	outMsg := dynamicpb.NewMessage(outDesc)
	uOpts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := uOpts.Unmarshal(respJSON, outMsg); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmarshal mock payload to proto: %v", err)
	}

	return outMsg, nil
}

func (m *MockServer) handleStream(srv interface{}, stream grpc.ServerStream) error {
	ctx := stream.Context()
	methodPath, ok := grpc.Method(ctx)
	if !ok {
		return status.Error(codes.Internal, "failed to get method from context")
	}

	op, opExists := m.spec.Operations[methodPath]
	if !opExists {
		return status.Errorf(codes.Unimplemented, "method %q not found in mock spec", methodPath)
	}

	methodDesc, descExists := m.methods[methodPath]
	if !descExists {
		return status.Errorf(codes.Unimplemented, "descriptor for %q not found", methodPath)
	}

	// 1. Evaluate Chaos Injection
	if triggered, err := m.evaluateChaos(stream); triggered {
		return err
	}

	isClientStream := methodDesc.IsStreamingClient()
	isServerStream := methodDesc.IsStreamingServer()

	if !isClientStream && !isServerStream {
		// Unary
		inDesc := methodDesc.Input()
		inMsg := dynamicpb.NewMessage(inDesc)
		if err := stream.RecvMsg(inMsg); err != nil {
			return err
		}

		respVal, gErr := m.processMessageAndValidate(ctx, op, methodDesc, inMsg, stream)
		if gErr != nil {
			return gErr
		}
		return stream.SendMsg(respVal)
	}

	if isClientStream && !isServerStream {
		// Client Streaming
		inDesc := methodDesc.Input()
		var lastMsg *dynamicpb.Message
		for {
			inMsg := dynamicpb.NewMessage(inDesc)
			err := stream.RecvMsg(inMsg)
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}

			// Validate each request
			mOpts := protojson.MarshalOptions{UseProtoNames: true}
			jsonBytes, err := mOpts.Marshal(inMsg)
			if err != nil {
				return status.Errorf(codes.Internal, "failed to marshal request message: %v", err)
			}
			var val interface{}
			if err := json.Unmarshal(jsonBytes, &val); err != nil {
				return status.Errorf(codes.Internal, "failed to parse request JSON: %v", err)
			}
			if err := op.Input.Match(val); err != nil {
				return status.Errorf(codes.InvalidArgument, "validation failed: %v", err)
			}
			lastMsg = inMsg
		}

		if lastMsg == nil {
			lastMsg = dynamicpb.NewMessage(inDesc)
		}

		respVal, gErr := m.processMessageAndValidate(ctx, op, methodDesc, lastMsg, stream)
		if gErr != nil {
			return gErr
		}
		return stream.SendMsg(respVal)
	}

	if !isClientStream && isServerStream {
		// Server Streaming
		inDesc := methodDesc.Input()
		inMsg := dynamicpb.NewMessage(inDesc)
		if err := stream.RecvMsg(inMsg); err != nil {
			return err
		}

		// Validate request
		mOpts := protojson.MarshalOptions{UseProtoNames: true}
		jsonBytes, err := mOpts.Marshal(inMsg)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to marshal request message: %v", err)
		}
		var val interface{}
		if err := json.Unmarshal(jsonBytes, &val); err != nil {
			return status.Errorf(codes.Internal, "failed to parse request JSON: %v", err)
		}
		if err := op.Input.Match(val); err != nil {
			return status.Errorf(codes.InvalidArgument, "validation failed: %v", err)
		}

		// Send 2 streaming responses
		for i := 0; i < 2; i++ {
			respVal, gErr := m.processMessageAndValidate(ctx, op, methodDesc, inMsg, stream)
			if gErr != nil {
				return gErr
			}
			if err := stream.SendMsg(respVal); err != nil {
				return err
			}
		}
		return nil
	}

	// Bidirectional Streaming
	inDesc := methodDesc.Input()
	for {
		inMsg := dynamicpb.NewMessage(inDesc)
		err := stream.RecvMsg(inMsg)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		respVal, gErr := m.processMessageAndValidate(ctx, op, methodDesc, inMsg, stream)
		if gErr != nil {
			return gErr
		}
		if err := stream.SendMsg(respVal); err != nil {
			return err
		}
	}
	return nil
}

func (m *MockServer) evaluateChaos(stream grpc.ServerStream) (bool, error) {
	latencyMs := 0
	latencyJitterMs := 0
	errorRate := 0.0
	errorStatus := 500
	dropConnectionRate := 0.0

	if m.config.Chaos != nil {
		latencyMs = m.config.Chaos.LatencyMs
		latencyJitterMs = m.config.Chaos.LatencyJitterMs
		errorRate = m.config.Chaos.ErrorRate
		errorStatus = m.config.Chaos.ErrorStatus
		dropConnectionRate = m.config.Chaos.DropConnectionRate
	}

	ctx := stream.Context()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-chaos-delay"); len(vals) > 0 {
			if val, err := strconv.Atoi(vals[0]); err == nil {
				latencyMs = val
			} else if dur, err := time.ParseDuration(vals[0]); err == nil {
				latencyMs = int(dur.Milliseconds())
			}
		} else if vals := md.Get("x-chaos-latency"); len(vals) > 0 {
			if val, err := strconv.Atoi(vals[0]); err == nil {
				latencyMs = val
			} else if dur, err := time.ParseDuration(vals[0]); err == nil {
				latencyMs = int(dur.Milliseconds())
			}
		}

		if vals := md.Get("x-chaos-delay-jitter"); len(vals) > 0 {
			if val, err := strconv.Atoi(vals[0]); err == nil {
				latencyJitterMs = val
			} else if dur, err := time.ParseDuration(vals[0]); err == nil {
				latencyJitterMs = int(dur.Milliseconds())
			}
		} else if vals := md.Get("x-chaos-latency-jitter"); len(vals) > 0 {
			if val, err := strconv.Atoi(vals[0]); err == nil {
				latencyJitterMs = val
			} else if dur, err := time.ParseDuration(vals[0]); err == nil {
				latencyJitterMs = int(dur.Milliseconds())
			}
		}

		if vals := md.Get("x-chaos-error-rate"); len(vals) > 0 {
			if val, err := strconv.ParseFloat(vals[0], 64); err == nil {
				errorRate = val
			}
		}

		if vals := md.Get("x-chaos-error-status"); len(vals) > 0 {
			if val, err := strconv.Atoi(vals[0]); err == nil {
				errorStatus = val
			}
		}

		if vals := md.Get("x-chaos-drop-rate"); len(vals) > 0 {
			if val, err := strconv.ParseFloat(vals[0], 64); err == nil {
				dropConnectionRate = val
			}
		} else if vals := md.Get("x-chaos-drop-connection-rate"); len(vals) > 0 {
			if val, err := strconv.ParseFloat(vals[0], 64); err == nil {
				dropConnectionRate = val
			}
		}
	}

	if dropConnectionRate > 0 {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		if rng.Float64() < dropConnectionRate {
			return true, status.Error(codes.Unavailable, "Simulated connection drop")
		}
	}

	if latencyMs > 0 {
		delay := time.Duration(latencyMs) * time.Millisecond
		if latencyJitterMs > 0 {
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			jitter := rng.Intn(latencyJitterMs*2) - latencyJitterMs
			delay += time.Duration(jitter) * time.Millisecond
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}

	if errorRate > 0 {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		if rng.Float64() < errorRate {
			gCode := m.mapHTTPStatusToGRPC(errorStatus)
			return true, status.Errorf(gCode, "Simulated chaos error with status %d", errorStatus)
		}
	}

	return false, nil
}

func (m *MockServer) mapHTTPStatusToGRPC(httpStatus int) codes.Code {
	if httpStatus >= 1 && httpStatus <= 16 {
		return codes.Code(httpStatus)
	}
	switch httpStatus {
	case 200:
		return codes.OK
	case 400:
		return codes.InvalidArgument
	case 401:
		return codes.Unauthenticated
	case 403:
		return codes.PermissionDenied
	case 404:
		return codes.NotFound
	case 409:
		return codes.Aborted
	case 429:
		return codes.ResourceExhausted
	case 500:
		return codes.Internal
	case 501:
		return codes.Unimplemented
	case 503:
		return codes.Unavailable
	case 504:
		return codes.DeadlineExceeded
	default:
		return codes.Internal
	}
}

func (m *MockServer) getScenarioResponse(op core.Operation, scenarioName string) (int, interface{}, map[string]interface{}, bool) {
	var statusVal int
	var body interface{}
	var headers map[string]interface{}
	found := false

	parseScenarioDef := func(def interface{}) bool {
		mDef, ok := def.(map[string]interface{})
		if !ok {
			return false
		}
		if sVal, ok := mDef["status"]; ok {
			switch v := sVal.(type) {
			case float64:
				statusVal = int(v)
			case int:
				statusVal = v
			case int64:
				statusVal = int(v)
			case string:
				if st, err := strconv.Atoi(v); err == nil {
					statusVal = st
				}
			}
		}
		if bVal, ok := mDef["body"]; ok {
			body = bVal
		}
		if hVal, ok := mDef["headers"]; ok {
			if hm, ok := hVal.(map[string]interface{}); ok {
				headers = hm
			}
		}
		return true
	}

	if m.config.ProtocolConfig != nil {
		if ops, ok := m.config.ProtocolConfig["operations"].(map[string]interface{}); ok {
			if opDef, ok := ops[op.ID].(map[string]interface{}); ok {
				if scs, ok := opDef["scenarios"].(map[string]interface{}); ok {
					if scDef, ok := scs[scenarioName]; ok {
						if parseScenarioDef(scDef) {
							found = true
						}
					}
				}
			}
		}
		if !found {
			if scs, ok := m.config.ProtocolConfig["scenarios"].(map[string]interface{}); ok {
				if scDef, ok := scs[scenarioName]; ok {
					if parseScenarioDef(scDef) {
						found = true
					}
				}
			}
		}
	}

	if !found && op.Metadata != nil {
		statusKey := fmt.Sprintf("scenario:%s:status", scenarioName)
		bodyKey := fmt.Sprintf("scenario:%s:body", scenarioName)
		headersKey := fmt.Sprintf("scenario:%s:headers", scenarioName)

		if stStr, ok := op.Metadata[statusKey]; ok {
			if st, err := strconv.Atoi(stStr); err == nil {
				statusVal = st
				found = true
			}
		}
		if bodyStr, ok := op.Metadata[bodyKey]; ok {
			var jsonVal interface{}
			if err := json.Unmarshal([]byte(bodyStr), &jsonVal); err == nil {
				body = jsonVal
			} else {
				body = bodyStr
			}
			found = true
		}
		if hStr, ok := op.Metadata[headersKey]; ok {
			var hMap map[string]interface{}
			if err := json.Unmarshal([]byte(hStr), &hMap); err == nil {
				headers = hMap
			}
		}
	}

	if !found {
		switch scenarioName {
		case "not-found":
			statusVal = 5 // codes.NotFound
			found = true
		case "server-error":
			statusVal = 13 // codes.Internal
			found = true
		}
	}

	return statusVal, body, headers, found
}

func generateMockValue(s core.Schema) interface{} {
	switch s.Type {
	case core.TypeScalar:
		switch s.ScalarType {
		case core.ScalarString:
			for _, c := range s.Constraints {
				if c.Kind == "format" {
					switch c.Value {
					case "uuid":
						return "123e4567-e89b-12d3-a456-426614174000"
					case "date-time":
						return "2026-05-19T23:00:00Z"
					case "email":
						return "mock@example.com"
					}
				}
			}
			return "mock_string"

		case core.ScalarNumber:
			minVal := 42.0
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if val, err := strconv.ParseFloat(c.Value, 64); err == nil {
						minVal = val
					}
				}
			}
			return minVal

		case core.ScalarInteger:
			minVal := int64(42)
			for _, c := range s.Constraints {
				if c.Kind == "min" {
					if val, err := strconv.ParseInt(c.Value, 10, 64); err == nil {
						minVal = val
					}
				}
			}
			return minVal

		case core.ScalarBoolean:
			return true
		}

	case core.TypeEnum:
		if len(s.EnumValues) > 0 {
			return s.EnumValues[0]
		}
		return "mock_enum_value"

	case core.TypeArray:
		if s.Item != nil {
			return []interface{}{generateMockValue(*s.Item)}
		}
		return []interface{}{}

	case core.TypeObject:
		obj := make(map[string]interface{})
		for name, propSchema := range s.Properties {
			obj[name] = generateMockValue(propSchema)
		}
		return obj
	}

	return nil
}
