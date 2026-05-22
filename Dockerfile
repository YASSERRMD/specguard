# Stage 1: Build the React frontend
FROM node:20-slim AS frontend-builder
WORKDIR /build/web
COPY web/package*.json ./
RUN npm install
COPY web/ ./
RUN npm run build

# Stage 2: Build the Rust library
FROM rust:1.91-slim AS rust-builder
WORKDIR /build/rust
COPY rust/Cargo.toml rust/Cargo.lock ./
# Create dummy lib file to cache dependencies
RUN mkdir src && echo "pub fn dummy() {}" > src/lib.rs && cargo build --release && rm -rf src
COPY rust/src ./src
RUN touch src/lib.rs && cargo build --release

# Stage 3: Build the Go binary
FROM golang:1.25-bookworm AS go-builder
WORKDIR /build
# Copy compiled Rust static library to expected relative path
COPY --from=rust-builder /build/rust/target/release/libspecguard_ffi.a /build/rust/target/release/libspecguard_ffi.a
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=1 GOOS=linux go build -o bin/specguard ./cmd/specguard

# Stage 4: Runtime container
FROM debian:bookworm-slim
WORKDIR /app
# Install sqlite3, ca-certificates
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
# Copy binary
COPY --from=go-builder /build/bin/specguard /app/specguard
# Copy built frontend assets
COPY --from=frontend-builder /build/web/dist /app/web/dist

# Expose server port
EXPOSE 8080

# Run specguard server by default
ENV SPECGUARD_DB_DSN=/data/specguard.db
VOLUME /data

ENTRYPOINT ["/app/specguard"]
CMD ["server"]
