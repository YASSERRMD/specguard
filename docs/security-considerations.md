# Specguard Security Considerations

This document details the security features, limits, and recommendations for securing Project Specguard.

## API Key Authentication

Specguard includes API key authentication to protect core admin endpoints.

- **Protected Endpoints**: All endpoints starting with `/api/*` (including spec uploads, starting/stopping mocks, and contract runs).
- **Unprotected Endpoints**: `/health` (liveness checks), `/metrics` (Prometheus metrics), and the frontend dashboard assets served at root `/`.
- **How to Configure**: Set the environment variable `SPECGUARD_API_KEY` to a strong secret string.
- **Client Usage**: Clients must include this key in HTTP requests as a Bearer token or custom header:
  - Header option 1: `Authorization: Bearer <your-api-key>`
  - Header option 2: `X-API-Key: <your-api-key>`

If `SPECGUARD_API_KEY` is not set or empty, authentication is disabled and all endpoints are publicly accessible.

## Rate Limiting

To prevent Denial of Service (DoS) attacks or resource exhaustion, Specguard mock engines enforce rate limiting.

- **Mock Servers**: Each active REST or gRPC mock server instance uses a thread-safe token bucket rate limiter.
- **Configurable Limits**: You can configure rate limits per mock through the mock configuration.
- **Defaults**: If not explicitly configured, mock servers default to a limit of 100 requests per second.
- **Over-limit Behavior**:
  - REST Mock: Returns `429 Too Many Requests`.
  - gRPC Mock: Returns status code `ResourceExhausted`.

## Payload Size Limits

To prevent memory exhaustion via extremely large payloads, Specguard enforces request body size limits.

- **REST Mock**: The mock server wraps incoming requests with a maximum bytes reader. Requests exceeding the limit are rejected before parsing.
- **gRPC Mock**: The server sets the maximum receive message size parameter.
- **Defaults**: The default limit is set to 10 Megabytes (10,485,760 bytes) unless overridden in the mock configuration.

## Upstream TLS/SSL Termination

The core Specguard server does not implement native TLS. For production environments, we recommend placing Specguard behind a reverse proxy or load balancer (such as Nginx, Caddy, or an AWS ALB) that terminates TLS and forwards requests over plain HTTP.
