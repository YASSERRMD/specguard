# Specguard Deployment Guide

This guide provides instructions for building, running, and deploying Project Specguard in production environments.

## Building from Source

To build Specguard from source, you need both Go and Rust toolchains installed on your system.

### 1. Build the Rust FFI Library

Specguard uses a Rust library for high performance structural diffs and hashing. Compile this library first:

```bash
cd rust
cargo build --release
```

This compiles a static library file (`libspecguard_ffi.a` or platform equivalent) in the `rust/target/release` directory.

### 2. Build the Go Binary

With the Rust FFI library built, compile the Go binary:

```bash
go build -o bin/specguard ./cmd/specguard
```

During build, Go links against the FFI library using CGO.

## Deployment with Docker

Specguard is packaged as a multi-stage Docker image that automates building the React dashboard, the Rust FFI library, and compiling the final Go server.

### Running with Docker

To build and run the Specguard Docker image:

```bash
docker build -t specguard:latest .
docker run -d \
  -p 8080:8080 \
  -v specguard-data:/data \
  -e SPECGUARD_API_KEY=your-secure-api-key \
  specguard:latest
```

## Deployment with Docker Compose

Using Docker Compose is the recommended way to run Specguard in production. It configures port mappings, persistent volume storage for the SQLite database, and env configurations.

### 1. Create a Compose File

Create a `docker-compose.yml` file:

```yaml
version: '3.8'

services:
  specguard:
    image: specguard:latest
    ports:
      - "8080:8080"
    environment:
      - SPECGUARD_PORT=8080
      - SPECGUARD_DB_DSN=/data/specguard.db
      - SPECGUARD_LOG_LEVEL=info
      - SPECGUARD_API_KEY=your-secure-api-key
    volumes:
      - specguard-data:/data
    restart: unless-stopped

volumes:
  specguard-data:
```

### 2. Start the Service

Start the service in detached mode:

```bash
docker compose up -d
```

## Configuration Parameters

Specguard supports configuration via environment variables:

| Environment Variable | Default Value | Description |
| :--- | :--- | :--- |
| `SPECGUARD_PORT` | `8080` | The port the HTTP API server listens on |
| `SPECGUARD_DB_DSN` | `specguard.db` | Path to the SQLite database file |
| `SPECGUARD_LOG_LEVEL` | `info` | Logging verbosity (debug, info, warn, error) |
| `SPECGUARD_API_KEY` | (None) | Key to secure API routes; if unset, auth is disabled |
