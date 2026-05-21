.PHONY: all build test lint run clean build-rust

all: build

build-rust:
	cd rust && cargo build --release

build: build-rust
	go build -o bin/specguard ./cmd/specguard

test: build-rust
	go test -v ./...

lint:
	go vet ./...
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "Go files not formatted:"; \
		gofmt -l .; \
		exit 1; \
	fi

run: build-rust
	go run ./cmd/specguard

clean:
	rm -rf bin/
	cd rust && cargo clean
