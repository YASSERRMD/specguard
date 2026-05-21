package grpc

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/specguard/internal/core"
	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const testProto = `
syntax = "proto3";
package test;

enum SearchType {
  DEFAULT = 0;
  ADVANCED = 1;
}

message SearchRequest {
  string query = 1;
  int32 page_number = 2;
  SearchType type = 3;
}

message SearchResponse {
  message Result {
    string url = 1;
    string title = 2;
  }
  repeated Result results = 1;
  string message = 2;
}

service SearchService {
  rpc Search(SearchRequest) returns (SearchResponse);
}
`

func TestGRPCAdapter_LoadSpec(t *testing.T) {
	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(testProto))
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	if len(spec.Operations) != 1 {
		t.Errorf("expected 1 operation, got %d", len(spec.Operations))
	}

	opID := "/test.SearchService/Search"
	op, ok := spec.Operations[opID]
	if !ok {
		t.Fatalf("operation %s not found", opID)
	}

	if op.Metadata["protocol"] != "grpc" {
		t.Errorf("expected protocol to be grpc, got %s", op.Metadata["protocol"])
	}
	if op.Metadata["service"] != "test.SearchService" {
		t.Errorf("expected service metadata test.SearchService, got %s", op.Metadata["service"])
	}
	if op.Metadata["method"] != "Search" {
		t.Errorf("expected method metadata Search, got %s", op.Metadata["method"])
	}
	if !strings.Contains(op.Metadata["proto_source"], "SearchService") {
		t.Errorf("proto_source metadata does not contain service definition")
	}

	// Verify input schema
	if op.Input.Type != core.TypeObject {
		t.Errorf("expected input type object, got %s", op.Input.Type)
	}
	querySchema, exists := op.Input.Properties["query"]
	if !exists {
		t.Errorf("expected input property 'query' to exist")
	}
	if querySchema.Type != core.TypeScalar || querySchema.ScalarType != core.ScalarString {
		t.Errorf("query field schema mismatch")
	}

	typeSchema, exists := op.Input.Properties["type"]
	if !exists {
		t.Errorf("expected input property 'type' to exist")
	}
	if typeSchema.Type != core.TypeEnum {
		t.Errorf("expected enum type for 'type' field, got %s", typeSchema.Type)
	}
	if len(typeSchema.EnumValues) != 2 || typeSchema.EnumValues[0] != "DEFAULT" {
		t.Errorf("enum values mismatch: %v", typeSchema.EnumValues)
	}

	// Verify output schema
	successSchema, exists := op.Output.Properties["0"]
	if !exists {
		t.Errorf("expected success status '0' in output properties")
	}
	if successSchema.Type != core.TypeObject {
		t.Errorf("expected output success schema type object, got %s", successSchema.Type)
	}
}

func TestGRPCMockServer_EndToEnd(t *testing.T) {
	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(testProto))
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	cfg := core.MockConfig{
		Host: "127.0.0.1",
		Port: 0,
	}

	runnableMock, err := adapter.GenerateMock(spec, cfg)
	if err != nil {
		t.Fatalf("GenerateMock failed: %v", err)
	}

	if err := runnableMock.Start(); err != nil {
		t.Fatalf("mock.Start failed: %v", err)
	}
	defer runnableMock.Stop()

	addr := runnableMock.GetAddress()
	if addr == "" {
		t.Fatalf("mock address is empty")
	}

	// Recompile proto locally in test to get input/output descriptors for calling
	grpcMock := runnableMock.(*MockServer)
	methodDesc, exists := grpcMock.methods["/test.SearchService/Search"]
	if !exists {
		t.Fatalf("method descriptor not found in mock server")
	}

	// Establish client connection
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial mock server: %v", err)
	}
	defer conn.Close()

	t.Run("Normal call", func(t *testing.T) {
		inMsg := dynamicpb.NewMessage(methodDesc.Input())
		inMsg.Set(methodDesc.Input().Fields().ByName("query"), protoreflect.ValueOfString("golang"))
		inMsg.Set(methodDesc.Input().Fields().ByName("page_number"), protoreflect.ValueOfInt32(2))
		inMsg.Set(methodDesc.Input().Fields().ByName("type"), protoreflect.ValueOfEnum(0)) // DEFAULT

		outMsg := dynamicpb.NewMessage(methodDesc.Output())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err = conn.Invoke(ctx, "/test.SearchService/Search", inMsg, outMsg)
		if err != nil {
			t.Fatalf("Invoke failed: %v", err)
		}

		msgVal := outMsg.Get(methodDesc.Output().Fields().ByName("message")).String()
		if msgVal == "" {
			t.Errorf("expected non-empty message in response")
		}

		resultsVal := outMsg.Get(methodDesc.Output().Fields().ByName("results")).List()
		if resultsVal.Len() != 1 {
			t.Errorf("expected 1 result in array, got %d", resultsVal.Len())
		}
	})

	t.Run("Validation failure", func(t *testing.T) {
		// We dynamically modify the input spec to require 'query' field
		op := spec.Operations["/test.SearchService/Search"]
		op.Input.Required = []string{"query"}
		spec.Operations["/test.SearchService/Search"] = op

		inMsg := dynamicpb.NewMessage(methodDesc.Input())
		// omit query field
		inMsg.Set(methodDesc.Input().Fields().ByName("page_number"), protoreflect.ValueOfInt32(2))

		outMsg := dynamicpb.NewMessage(methodDesc.Output())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err = conn.Invoke(ctx, "/test.SearchService/Search", inMsg, outMsg)
		if err == nil {
			t.Fatalf("expected validation failure but invoke succeeded")
		}

		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument status, got %v", err)
		}
	})

	t.Run("Scenario Selection: not-found", func(t *testing.T) {
		inMsg := dynamicpb.NewMessage(methodDesc.Input())
		inMsg.Set(methodDesc.Input().Fields().ByName("query"), protoreflect.ValueOfString("anything"))

		outMsg := dynamicpb.NewMessage(methodDesc.Output())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		ctx = metadata.AppendToOutgoingContext(ctx, "x-mock-scenario", "not-found")
		err = conn.Invoke(ctx, "/test.SearchService/Search", inMsg, outMsg)
		if err == nil {
			t.Fatalf("expected not-found scenario error but invoke succeeded")
		}

		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.NotFound {
			t.Errorf("expected NotFound status code, got %v", err)
		}
	})

	t.Run("Scenario Selection: server-error", func(t *testing.T) {
		inMsg := dynamicpb.NewMessage(methodDesc.Input())
		inMsg.Set(methodDesc.Input().Fields().ByName("query"), protoreflect.ValueOfString("anything"))

		outMsg := dynamicpb.NewMessage(methodDesc.Output())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		ctx = metadata.AppendToOutgoingContext(ctx, "x-mock-scenario", "server-error")
		err = conn.Invoke(ctx, "/test.SearchService/Search", inMsg, outMsg)
		if err == nil {
			t.Fatalf("expected server-error scenario error but invoke succeeded")
		}

		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Internal {
			t.Errorf("expected Internal status code, got %v", err)
		}
	})

	t.Run("Chaos Injection: Latency", func(t *testing.T) {
		inMsg := dynamicpb.NewMessage(methodDesc.Input())
		inMsg.Set(methodDesc.Input().Fields().ByName("query"), protoreflect.ValueOfString("anything"))

		outMsg := dynamicpb.NewMessage(methodDesc.Output())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		ctx = metadata.AppendToOutgoingContext(ctx, "x-chaos-latency", "50ms")
		start := time.Now()
		err = conn.Invoke(ctx, "/test.SearchService/Search", inMsg, outMsg)
		duration := time.Since(start)

		if err != nil {
			t.Fatalf("Invoke failed: %v", err)
		}
		if duration < 50*time.Millisecond {
			t.Errorf("expected latency injection of 50ms, request took only %v", duration)
		}
	})

	t.Run("Chaos Injection: Error status", func(t *testing.T) {
		inMsg := dynamicpb.NewMessage(methodDesc.Input())
		inMsg.Set(methodDesc.Input().Fields().ByName("query"), protoreflect.ValueOfString("anything"))

		outMsg := dynamicpb.NewMessage(methodDesc.Output())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		ctx = metadata.AppendToOutgoingContext(ctx, "x-chaos-error-rate", "1.0", "x-chaos-error-status", "14") // 14 is codes.Unavailable
		err = conn.Invoke(ctx, "/test.SearchService/Search", inMsg, outMsg)
		if err == nil {
			t.Fatalf("expected error chaos but invoke succeeded")
		}

		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Unavailable {
			t.Errorf("expected Unavailable status code, got %v", err)
		}
	})

	t.Run("Chaos Injection: Connection drop", func(t *testing.T) {
		inMsg := dynamicpb.NewMessage(methodDesc.Input())
		inMsg.Set(methodDesc.Input().Fields().ByName("query"), protoreflect.ValueOfString("anything"))

		outMsg := dynamicpb.NewMessage(methodDesc.Output())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		ctx = metadata.AppendToOutgoingContext(ctx, "x-chaos-drop-rate", "1.0")
		err = conn.Invoke(ctx, "/test.SearchService/Search", inMsg, outMsg)
		if err == nil {
			t.Fatalf("expected drop connection chaos but invoke succeeded")
		}

		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Unavailable {
			t.Errorf("expected Unavailable status code from connection drop, got %v", err)
		}
	})
}

const streamingTestProto = `
syntax = "proto3";
package test;

message SimpleRequest {
  string value = 1;
}

message SimpleResponse {
  string result = 1;
}

service StreamService {
  rpc ClientStream(stream SimpleRequest) returns (SimpleResponse);
  rpc ServerStream(SimpleRequest) returns (stream SimpleResponse);
  rpc BidiStream(stream SimpleRequest) returns (stream SimpleResponse);
}
`

func TestGRPCMockServer_Streaming(t *testing.T) {
	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(streamingTestProto))
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	cfg := core.MockConfig{
		Host: "127.0.0.1",
		Port: 0,
	}

	runnableMock, err := adapter.GenerateMock(spec, cfg)
	if err != nil {
		t.Fatalf("GenerateMock failed: %v", err)
	}

	if err := runnableMock.Start(); err != nil {
		t.Fatalf("mock.Start failed: %v", err)
	}
	defer runnableMock.Stop()

	addr := runnableMock.GetAddress()
	if addr == "" {
		t.Fatalf("mock address is empty")
	}

	grpcMock := runnableMock.(*MockServer)
	clientStreamDesc, exists := grpcMock.methods["/test.StreamService/ClientStream"]
	if !exists {
		t.Fatalf("client stream method descriptor not found")
	}
	serverStreamDesc := grpcMock.methods["/test.StreamService/ServerStream"]
	bidiStreamDesc := grpcMock.methods["/test.StreamService/BidiStream"]

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial mock server: %v", err)
	}
	defer conn.Close()

	t.Run("Client Streaming Mock", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		desc := &grpc.StreamDesc{
			StreamName:    "ClientStream",
			ClientStreams: true,
		}
		str, err := conn.NewStream(ctx, desc, "/test.StreamService/ClientStream")
		if err != nil {
			t.Fatalf("NewStream failed: %v", err)
		}

		inMsg := dynamicpb.NewMessage(clientStreamDesc.Input())
		inMsg.Set(clientStreamDesc.Input().Fields().ByName("value"), protoreflect.ValueOfString("hello"))

		// Send two messages
		if err := str.SendMsg(inMsg); err != nil {
			t.Fatalf("SendMsg failed: %v", err)
		}
		if err := str.SendMsg(inMsg); err != nil {
			t.Fatalf("SendMsg failed: %v", err)
		}
		if err := str.CloseSend(); err != nil {
			t.Fatalf("CloseSend failed: %v", err)
		}

		outMsg := dynamicpb.NewMessage(clientStreamDesc.Output())
		if err := str.RecvMsg(outMsg); err != nil {
			t.Fatalf("RecvMsg failed: %v", err)
		}

		resVal := outMsg.Get(clientStreamDesc.Output().Fields().ByName("result")).String()
		if resVal == "" {
			t.Errorf("expected non-empty result")
		}
	})

	t.Run("Server Streaming Mock", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		desc := &grpc.StreamDesc{
			StreamName:    "ServerStream",
			ServerStreams: true,
		}
		str, err := conn.NewStream(ctx, desc, "/test.StreamService/ServerStream")
		if err != nil {
			t.Fatalf("NewStream failed: %v", err)
		}

		inMsg := dynamicpb.NewMessage(serverStreamDesc.Input())
		inMsg.Set(serverStreamDesc.Input().Fields().ByName("value"), protoreflect.ValueOfString("hello"))

		if err := str.SendMsg(inMsg); err != nil {
			t.Fatalf("SendMsg failed: %v", err)
		}
		if err := str.CloseSend(); err != nil {
			t.Fatalf("CloseSend failed: %v", err)
		}

		var results []string
		for {
			outMsg := dynamicpb.NewMessage(serverStreamDesc.Output())
			err := str.RecvMsg(outMsg)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("RecvMsg failed: %v", err)
			}
			results = append(results, outMsg.Get(serverStreamDesc.Output().Fields().ByName("result")).String())
		}

		// Mock server streams back exactly 2 generated responses
		if len(results) != 2 {
			t.Errorf("expected exactly 2 responses, got %d", len(results))
		}
	})

	t.Run("Bidirectional Streaming Mock", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		desc := &grpc.StreamDesc{
			StreamName:    "BidiStream",
			ClientStreams: true,
			ServerStreams: true,
		}
		str, err := conn.NewStream(ctx, desc, "/test.StreamService/BidiStream")
		if err != nil {
			t.Fatalf("NewStream failed: %v", err)
		}

		inMsg := dynamicpb.NewMessage(bidiStreamDesc.Input())
		inMsg.Set(bidiStreamDesc.Input().Fields().ByName("value"), protoreflect.ValueOfString("hello"))

		// Iteration 1: send and recv
		if err := str.SendMsg(inMsg); err != nil {
			t.Fatalf("SendMsg failed: %v", err)
		}
		outMsg1 := dynamicpb.NewMessage(bidiStreamDesc.Output())
		if err := str.RecvMsg(outMsg1); err != nil {
			t.Fatalf("RecvMsg failed: %v", err)
		}
		if outMsg1.Get(bidiStreamDesc.Output().Fields().ByName("result")).String() == "" {
			t.Errorf("expected result in response 1")
		}

		// Iteration 2: send and recv
		if err := str.SendMsg(inMsg); err != nil {
			t.Fatalf("SendMsg failed: %v", err)
		}
		outMsg2 := dynamicpb.NewMessage(bidiStreamDesc.Output())
		if err := str.RecvMsg(outMsg2); err != nil {
			t.Fatalf("RecvMsg failed: %v", err)
		}
		if outMsg2.Get(bidiStreamDesc.Output().Fields().ByName("result")).String() == "" {
			t.Errorf("expected result in response 2")
		}

		if err := str.CloseSend(); err != nil {
			t.Fatalf("CloseSend failed: %v", err)
		}

		// Recv until EOF
		outMsg3 := dynamicpb.NewMessage(bidiStreamDesc.Output())
		err = str.RecvMsg(outMsg3)
		if err != io.EOF {
			t.Errorf("expected EOF at the end of stream, got %v", err)
		}
	})
}

func TestGRPCAdapter_RunContractChecks(t *testing.T) {
	adapter := NewAdapter()
	spec, err := adapter.LoadSpec([]byte(streamingTestProto))
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	t.Run("Conformant SUT", func(t *testing.T) {
		cfg := core.MockConfig{
			Host: "127.0.0.1",
			Port: 0,
		}
		mockSUT, err := adapter.GenerateMock(spec, cfg)
		if err != nil {
			t.Fatalf("GenerateMock failed: %v", err)
		}
		if err := mockSUT.Start(); err != nil {
			t.Fatalf("failed to start mock SUT: %v", err)
		}
		defer mockSUT.Stop()

		// Run contract checks against mock server
		res, err := adapter.RunContractChecks(spec, mockSUT.GetAddress())
		if err != nil {
			t.Fatalf("RunContractChecks failed: %v", err)
		}

		if !res.Passed {
			t.Errorf("expected contract checks to pass, but failed with findings: %+v", res.DriftReport.Findings)
		}
	})

	t.Run("Drifting SUT: Unexpected status code", func(t *testing.T) {
		// Start a custom gRPC server that returns undocumented error code
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen: %v", err)
		}
		defer lis.Close()

		srv := grpc.NewServer(
			grpc.UnknownServiceHandler(func(srv interface{}, stream grpc.ServerStream) error {
				return status.Error(codes.InvalidArgument, "invalid argument simulation")
			}),
		)
		go func() {
			_ = srv.Serve(lis)
		}()
		defer srv.Stop()

		res, err := adapter.RunContractChecks(spec, lis.Addr().String())
		if err != nil {
			t.Fatalf("RunContractChecks failed: %v", err)
		}

		if res.Passed {
			t.Errorf("expected contract checks to fail, but it passed")
		}

		// Look for finding for missing status code 3 (InvalidArgument)
		foundDrift := false
		for _, f := range res.DriftReport.Findings {
			if f.Kind == core.KindMissing && strings.Contains(f.Location, "output.3") {
				foundDrift = true
				break
			}
		}
		if !foundDrift {
			t.Errorf("expected missing status code drift finding, got: %+v", res.DriftReport.Findings)
		}
	})

	t.Run("Drifting SUT: Schema constraint mismatch (missing required field)", func(t *testing.T) {
		// Modify spec to require "result" field
		op := spec.Operations["/test.StreamService/ClientStream"]
		successSchema := op.Output.Properties["0"]
		successSchema.Required = []string{"result"}
		op.Output.Properties["0"] = successSchema
		spec.Operations["/test.StreamService/ClientStream"] = op

		// Start a custom gRPC server that returns success but empty SimpleResponse{}
		// protojson will omit "result" from the serialized JSON because it has default value (empty string)
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen: %v", err)
		}
		defer lis.Close()

		// Recompile proto to get descriptor to send message
		// Compile proto in-memory
		files := map[string]string{
			"input.proto": streamingTestProto,
		}
		compiler := protocompile.Compiler{
			Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
				Accessor: protocompile.SourceAccessorFromMap(files),
			}),
		}
		fds, err := compiler.Compile(context.Background(), "input.proto")
		if err != nil {
			t.Fatalf("failed to compile: %v", err)
		}
		fd := fds[0]
		clientStreamMethod := fd.Services().Get(0).Methods().Get(0)

		srv := grpc.NewServer(
			grpc.UnknownServiceHandler(func(srv interface{}, stream grpc.ServerStream) error {
				// Receive incoming request to keep stream correct
				inMsg := dynamicpb.NewMessage(clientStreamMethod.Input())
				for {
					err := stream.RecvMsg(inMsg)
					if err == io.EOF {
						break
					}
					if err != nil {
						return err
					}
				}
				// Send empty SimpleResponse
				outMsg := dynamicpb.NewMessage(clientStreamMethod.Output())
				return stream.SendMsg(outMsg)
			}),
		)
		go func() {
			_ = srv.Serve(lis)
		}()
		defer srv.Stop()

		res, err := adapter.RunContractChecks(spec, lis.Addr().String())
		if err != nil {
			t.Fatalf("RunContractChecks failed: %v", err)
		}

		if res.Passed {
			t.Errorf("expected contract checks to fail due to missing required field")
		}

		foundDrift := false
		for _, f := range res.DriftReport.Findings {
			if f.Kind == core.KindMissing && strings.Contains(f.Location, "output.0.result") {
				foundDrift = true
				break
			}
		}
		if !foundDrift {
			t.Errorf("expected missing required field finding under output.0.result, got: %+v", res.DriftReport.Findings)
		}
	})
}
