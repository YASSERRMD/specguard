# Protocol Adapter Interface Design

This document details the interface and architectural boundaries between the protocol-neutral core of Specguard and its protocol-specific adapters (such as REST/OpenAPI or gRPC/Protobuf).

## Architectural Boundary

The primary goal of Specguard is to build a protocol-agnostic mocking and contract-testing engine. To achieve this, all protocol details must be handled entirely within the adapter layer. The core engine only operates on normalized specification models.

### Preventing REST and gRPC Leaks

REST assumptions (such as HTTP paths, query parameters, header names, and request methods) and gRPC assumptions (such as Protobuf field numbers, message types, metadata, or streaming channels) must never leak into the `internal/core` type declarations.

Instead, the core uses high-level, protocol-neutral abstractions:
1. **Operations**: Named actions or endpoints representing any single request-response invocation (for example, a REST endpoint like `GET /users` or a gRPC method like `GetUser`).
2. **Shapes (Schema)**: Represented via standard JSON-compatible types (`object`, `array`, `scalar`, `enum`). Regardless of the source (XML, JSON, Protobuf binary, URL parameters), all request and response structures are mapped to a Normalized Schema.
3. **Constraints**: Generic validators (like `pattern` or `min`) that represent value-specific assertions, rather than specific protocol details.

## The ProtocolAdapter Interface

Adapters are registered with the engine and implement the `ProtocolAdapter` interface defined in `internal/core/adapter.go`:

```go
type ProtocolAdapter interface {
	LoadSpec(source []byte) (*NormalizedSpec, error)
	GenerateMock(spec *NormalizedSpec, config MockConfig) (RunnableMock, error)
	RunContractChecks(spec *NormalizedSpec, targetURL string) (CheckResult, error)
	NormalizeResult(rawResult interface{}) (*DriftReport, error)
}
```

### 1. LoadSpec
This method takes a raw protocol-specific specification file (such as an OpenAPI JSON/YAML file or a `.proto` file) and translates it into a `NormalizedSpec`.
- For REST: Translates the combination of HTTP Path, Method, and Parameters into the input/output schemas of an Operation.
- For gRPC: Translates the RPC service definition and associated Protobuf messages into the input/output schemas of an Operation.

### 2. GenerateMock
This method stands up a running mock server configured to return mock values adhering to the output shapes specified by the `NormalizedSpec`.
- The mock server listens on the configured host and port.
- Any request received by the mock server is decoded by the adapter, validated against the input schema, and responded to using mock data generated from the output schema.

### 3. RunContractChecks
This method takes a normalized spec and makes real calls to an active system under test (SUT) listening at `targetURL`. It asserts that the system's responses match the expected output schemas and constraints.

### 4. NormalizeResult
This method converts raw validation outputs (such as HTTP response code differences or serialization errors) into a protocol-neutral `DriftReport`. This ensures that downstream tools (including CLI reports and the web dashboard) do not have to understand protocol-specific details to render findings.
