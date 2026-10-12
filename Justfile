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
