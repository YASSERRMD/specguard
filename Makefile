.PHONY: all build test lint run clean

all: build

build:
	go build -o bin/specguard ./cmd/specguard

test:
	go test -v ./...

lint:
	go vet ./...
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "Go files not formatted:"; \
		gofmt -l .; \
		exit 1; \
	fi

run:
	go run ./cmd/specguard

clean:
	rm -rf bin/
