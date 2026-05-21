package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/specguard/internal/core"
	"github.com/YASSERRMD/specguard/internal/server"
	"github.com/YASSERRMD/specguard/internal/store"
	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const e2eTestGRPCProto = `syntax = "proto3";
package test;

option go_package = "github.com/YASSERRMD/specguard/internal/adapters/grpc/test;test";

message UserRequest {
  string id = 1;
}

message UserResponse {
  string id = 2;
  string name = 3;
}

service UserService {
  rpc GetUser(UserRequest) returns (UserResponse);
  rpc StreamUsers(UserRequest) returns (stream UserResponse);
}
`

func TestGRPCPath_EndToEnd(t *testing.T) {
	// 1. Create a temporary directory and SQLite database file
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "specguard_e2e_grpc.db")

	cfg := &server.Config{
		Port:     "0", // random port
		DBDSN:    dbPath,
		LogLevel: "info",
	}

	dbStore, err := store.NewSQLiteStore(cfg.DBDSN)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer dbStore.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.NewServer(cfg, dbStore, logger)

	apiSrv := httptest.NewServer(srv.Handler())
	defer apiSrv.Close()
	t.Cleanup(func() {
		_ = srv.Stop(context.Background())
	})

	// Wait for server to be healthy
	healthy := false
	for i := 0; i < 10; i++ {
		resp, err := http.Get(apiSrv.URL + "/health")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			healthy = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthy {
		t.Fatal("Specguard API server failed to become healthy")
	}

	// 2. Write Protobuf spec to temporary file
	specFile := filepath.Join(tempDir, "e2e_spec.proto")
	err = os.WriteFile(specFile, []byte(e2eTestGRPCProto), 0644)
	if err != nil {
		t.Fatalf("failed to write spec file: %v", err)
	}

	// 3. Register spec via CLI
	out, err := execCLI(t, apiSrv.URL, "spec", "add", "grpc-e2e-spec", specFile)
	if err != nil {
		t.Fatalf("spec add failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "added successfully") {
		t.Errorf("expected spec add success, got: %s", out)
	}

	// 4. List specs via CLI
	out, err = execCLI(t, apiSrv.URL, "spec", "list")
	if err != nil {
		t.Fatalf("spec list failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, "- grpc-e2e-spec") {
		t.Errorf("expected 'grpc-e2e-spec' in spec list output, got: %s", out)
	}

	// 5. Start mock server via CLI
	out, err = execCLI(t, apiSrv.URL, "mock", "start", "grpc-e2e-spec")
	if err != nil {
		t.Fatalf("mock start failed: %v, output: %s", err, out)
	}
	if !strings.Contains(out, `"status":"started"`) {
		t.Errorf("expected status 'started' in mock start output, got: %s", out)
	}

	// Extract mock server address
	var startResult struct {
		Address string `json:"address"`
	}
	parts := strings.Split(out, "Response: ")
	if len(parts) >= 2 {
		jsonStr := parts[1]
		if idx := strings.LastIndex(jsonStr, "}"); idx != -1 {
			jsonStr = jsonStr[:idx+1]
		}
		_ = json.Unmarshal([]byte(jsonStr), &startResult)
	}
	if startResult.Address == "" {
		t.Fatalf("failed to extract mock address from output: %s", out)
	}

	// Compile the proto locally in test to invoke dynamic client calls
	files := map[string]string{
		"input.proto": e2eTestGRPCProto,
	}
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(files),
		}),
	}
	fds, err := compiler.Compile(context.Background(), "input.proto")
	if err != nil {
		t.Fatalf("failed to compile proto locally: %v", err)
	}
	fd := fds[0]
	methodDesc := fd.Services().Get(0).Methods().Get(0)       // GetUser
	streamMethodDesc := fd.Services().Get(0).Methods().Get(1) // StreamUsers

	// Establish client connection
	conn, err := grpc.Dial(startResult.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial mock server: %v", err)
	}
	defer conn.Close()

	// 6. Verify mock server responds to conforming request
	inMsg := dynamicpb.NewMessage(methodDesc.Input())
	inMsg.Set(methodDesc.Input().Fields().ByName("id"), protoreflect.ValueOfString("123"))

	outMsg := dynamicpb.NewMessage(methodDesc.Output())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = conn.Invoke(ctx, "/test.UserService/GetUser", inMsg, outMsg)
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	idVal := outMsg.Get(methodDesc.Output().Fields().ByName("id")).String()
	nameVal := outMsg.Get(methodDesc.Output().Fields().ByName("name")).String()
	if idVal == "" || nameVal == "" {
		t.Errorf("expected non-empty fields in mock response, got id=%q name=%q", idVal, nameVal)
	}

	// 6b. Verify mock server responds to streaming request
	ctxStream, cancelStream := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelStream()

	desc := &grpc.StreamDesc{
		StreamName:    "StreamUsers",
		ServerStreams: true,
	}
	str, err := conn.NewStream(ctxStream, desc, "/test.UserService/StreamUsers")
	if err != nil {
		t.Fatalf("NewStream failed: %v", err)
	}

	inMsgStream := dynamicpb.NewMessage(streamMethodDesc.Input())
	inMsgStream.Set(streamMethodDesc.Input().Fields().ByName("id"), protoreflect.ValueOfString("abc"))

	if err := str.SendMsg(inMsgStream); err != nil {
		t.Fatalf("SendMsg failed: %v", err)
	}
	if err := str.CloseSend(); err != nil {
		t.Fatalf("CloseSend failed: %v", err)
	}

	var streamResults []string
	for {
		outMsgStream := dynamicpb.NewMessage(streamMethodDesc.Output())
		err := str.RecvMsg(outMsgStream)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("RecvMsg failed: %v", err)
		}
		streamResults = append(streamResults, outMsgStream.Get(streamMethodDesc.Output().Fields().ByName("id")).String())
	}

	if len(streamResults) != 2 {
		t.Errorf("expected exactly 2 responses in stream, got %d", len(streamResults))
	}

	// 7. Verify scenario selection
	inMsgNF := dynamicpb.NewMessage(methodDesc.Input())
	inMsgNF.Set(methodDesc.Input().Fields().ByName("id"), protoreflect.ValueOfString("abc"))
	outMsgNF := dynamicpb.NewMessage(methodDesc.Output())

	ctxNF, cancelNF := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelNF()

	ctxNF = metadata.AppendToOutgoingContext(ctxNF, "x-mock-scenario", "not-found")
	err = conn.Invoke(ctxNF, "/test.UserService/GetUser", inMsgNF, outMsgNF)
	if err == nil {
		t.Fatalf("expected error from not-found scenario, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.NotFound {
		t.Errorf("expected NotFound status, got %v", err)
	}

	// 8. Verify chaos injection
	inMsgChaos := dynamicpb.NewMessage(methodDesc.Input())
	inMsgChaos.Set(methodDesc.Input().Fields().ByName("id"), protoreflect.ValueOfString("abc"))
	outMsgChaos := dynamicpb.NewMessage(methodDesc.Output())

	ctxChaos, cancelChaos := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelChaos()

	ctxChaos = metadata.AppendToOutgoingContext(ctxChaos, "x-chaos-latency", "50ms", "x-chaos-error-rate", "1.0", "x-chaos-error-status", "3")
	start := time.Now()
	err = conn.Invoke(ctxChaos, "/test.UserService/GetUser", inMsgChaos, outMsgChaos)
	duration := time.Since(start)

	if err == nil {
		t.Fatalf("expected error from chaos error rate, got nil")
	}
	st, ok = status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument status, got %v", err)
	}
	if duration < 50*time.Millisecond {
		t.Errorf("expected chaos latency to delay request by at least 50ms, took %v", duration)
	}

	// 9. Run contract checks against conforming mock SUT via CLI
	outCheck, err := execCLI(t, apiSrv.URL, "contract", "run", "grpc-e2e-spec", startResult.Address, "--format", "json")
	if err != nil {
		t.Fatalf("contract run against conforming mock SUT failed: %v, output: %s", err, outCheck)
	}
	if !strings.Contains(outCheck, `"passed": true`) {
		t.Errorf("expected contract checks to pass, got output: %s", outCheck)
	}

	runID := extractRunID(outCheck)
	if runID == "" {
		t.Errorf("failed to extract run_id from contract run output: %s", outCheck)
	} else {
		outReport, err := execCLI(t, apiSrv.URL, "report", "show", runID)
		if err != nil {
			t.Fatalf("report show failed: %v, output: %s", err, outReport)
		}
		if !strings.Contains(outReport, `"findings":[]`) {
			t.Errorf("expected empty findings in conforming run report, got: %s", outReport)
		}
	}

	// 10. Run contract checks against drifting gRPC SUT
	// We first fetch the spec from the server to modify and require the "name" field
	respGet, err := http.Get(apiSrv.URL + "/api/specs?id=grpc-e2e-spec")
	if err != nil {
		t.Fatalf("failed to fetch spec from server: %v", err)
	}
	defer respGet.Body.Close()
	if respGet.StatusCode != http.StatusOK {
		t.Fatalf("failed to fetch spec, status: %d", respGet.StatusCode)
	}

	var loadedSpec core.NormalizedSpec
	err = json.NewDecoder(respGet.Body).Decode(&loadedSpec)
	if err != nil {
		t.Fatalf("failed to decode spec: %v", err)
	}

	// Modify spec to require "name" field in the success "0" response of GetUser
	opGetUser := loadedSpec.Operations["/test.UserService/GetUser"]
	successSchema := opGetUser.Output.Properties["0"]
	successSchema.Required = []string{"name"}
	opGetUser.Output.Properties["0"] = successSchema
	loadedSpec.Operations["/test.UserService/GetUser"] = opGetUser

	// Save back the modified spec via CLI
	modifiedSpecFile := filepath.Join(tempDir, "modified_spec.json")
	modifiedSpecBytes, err := json.Marshal(loadedSpec)
	if err != nil {
		t.Fatalf("failed to marshal modified spec: %v", err)
	}
	err = os.WriteFile(modifiedSpecFile, modifiedSpecBytes, 0644)
	if err != nil {
		t.Fatalf("failed to write modified spec file: %v", err)
	}

	outMod, err := execCLI(t, apiSrv.URL, "spec", "add", "grpc-e2e-spec", modifiedSpecFile)
	if err != nil {
		t.Fatalf("spec add modified failed: %v, output: %s", err, outMod)
	}

	// Start drifting SUT (a custom gRPC server that returns empty UserResponse, meaning name is omitted)
	driftLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer driftLis.Close()

	driftSrv := grpc.NewServer(
		grpc.UnknownServiceHandler(func(srv interface{}, stream grpc.ServerStream) error {
			inMsg := dynamicpb.NewMessage(methodDesc.Input())
			if err := stream.RecvMsg(inMsg); err != nil && err != io.EOF {
				return err
			}
			outMsg := dynamicpb.NewMessage(methodDesc.Output())
			return stream.SendMsg(outMsg)
		}),
	)
	go func() {
		_ = driftSrv.Serve(driftLis)
	}()
	defer driftSrv.Stop()

	// Run contract run via CLI against drifting SUT
	outDriftCheck, err := execCLI(t, apiSrv.URL, "contract", "run", "grpc-e2e-spec", driftLis.Addr().String(), "--format", "json")
	if err == nil {
		t.Fatalf("expected contract run to exit non-zero for drifting SUT, got exit 0. Output: %s", outDriftCheck)
	}
	if !strings.Contains(outDriftCheck, `"passed": false`) {
		t.Errorf("expected contract checks to fail, got output: %s", outDriftCheck)
	}

	driftRunID := extractRunID(outDriftCheck)
	if driftRunID == "" {
		t.Errorf("failed to extract run_id from drifting contract run: %s", outDriftCheck)
	} else {
		outReport, err := execCLI(t, apiSrv.URL, "report", "show", driftRunID)
		if err != nil {
			t.Fatalf("report show for drifting SUT failed: %v, output: %s", err, outReport)
		}
		if !strings.Contains(outReport, "name") || !strings.Contains(outReport, "findings") {
			t.Errorf("expected report to highlight missing field 'name', got: %s", outReport)
		}
	}

	// 11. Stop mock server via CLI
	outStop, err := execCLI(t, apiSrv.URL, "mock", "stop", "grpc-e2e-spec")
	if err != nil {
		t.Fatalf("mock stop failed: %v, output: %s", err, outStop)
	}
	if !strings.Contains(outStop, `"status":"stopped"`) {
		t.Errorf("expected mock stop success, got: %s", outStop)
	}

	// Verify mock server has stopped and connection fails
	_, err = grpc.Dial(startResult.Address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(), grpc.WithTimeout(50*time.Millisecond))
	if err == nil {
		t.Errorf("expected mock server connection to fail after stop, but succeeded")
	}
}
