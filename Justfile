# List available recipes
default:
    @just --list

# Build binaries
build:
    go build -o bin/ ./...

# Lint Go code using golangci-lint
lint:
    golangci-lint run ./...

# Run Go tests
test:
    go test -v ./...

# Run E2E tests against running docker compose environment
test-e2e:
    E2E=1 go test -v -count=1 ./e2e

# Format Go code using goimports
fmt:
    goimports -w -local "github.com/elmhuangyu/configs" .

# Run go mod tidy
tidy:
    go mod tidy

# Update Go dependencies and tidy
update-deps:
    go get -u ./...
    @just tidy

# Clean build artifacts
clean:
    rm -rf bin
