default:
    @just --list

# Build the ignit binary with version info from git
build:
    #!/usr/bin/env sh
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
    CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o ignit .

# Run all tests
test:
    go test ./...

# Run tests with the race detector
test-race:
    go test -race ./...

# Run go vet
vet:
    go vet ./...

# Format code
fmt:
    gofmt -w .

# Lint: golangci-lint (uses built-in defaults)
lint:
    golangci-lint run

# Clean build artifacts
clean:
    rm -f ignit
