# Specguard

Project Specguard is a protocol-pluggable API mocking and contract-testing tool designed to simplify testing of service boundaries. It enables teams to automatically stand up mock servers and verify downstream API conformance using normalized specification models. By isolating applications from their protocol details, Specguard prevents contract drift and ensures high-fidelity API simulation.

## Build Phases

1. **Phase 01**: Repo scaffold and CI
2. **Phase 02**: Normalized spec model and protocol adapter interface
3. **Phase 03**: SQLite data layer behind an interface
4. **Phase 04**: Core HTTP API and CLI skeleton
5. **Phase 05**: REST adapter: spec loading and validation
6. **Phase 06**: REST adapter: mock engine
7. **Phase 07**: Stateful mocking and scenario selection
8. **Phase 08**: Fault and chaos injection layer
9. **Phase 09**: Rust FFI: spec hasher and structural diff engine
10. **Phase 10**: REST adapter: contract runner and drift report
11. **Phase 11**: Web dashboard
12. **Phase 12**: End-to-end tests for the REST path
13. **Phase 13**: gRPC adapter: protobuf loading and mock
14. **Phase 14**: gRPC adapter: contract runner and streaming
15. **Phase 15**: gRPC end-to-end tests and adapter parity check
16. **Phase 16**: CI/CD integration and machine-readable output
17. **Phase 17**: Hardening, observability, and packaging
