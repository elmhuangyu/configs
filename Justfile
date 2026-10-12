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

# Fixed test age key used for examples
EXAMPLE_AGE_RECIPIENT := "age1lzd99uklcjnc0e7d860axevet2cz99ce9pq6tzuzd05l5nr28ams36nvun"
EXAMPLE_AGE_IDENTITY  := "AGE-SECRET-KEY-1G0Q5K9TV4REQ3ZSQRMTMG8NSWQGYT0T7TZ33RAZEE0GZYVZN0APSU24RK7"

# Compile and encrypt example configs into encrypted/SOPS format
example-encrypt: build
    rm -rf examples/configs-encrypted
    cp -r examples/configs-source examples/configs-encrypted
    ./bin/cfgctl encrypt examples/configs-encrypted/raw --age {{EXAMPLE_AGE_RECIPIENT}}
    ./bin/cfgctl encrypt examples/configs-encrypted/json --age {{EXAMPLE_AGE_RECIPIENT}}
    ./bin/cfgctl encrypt examples/configs-encrypted/yaml --age {{EXAMPLE_AGE_RECIPIENT}}
    ./scripts/prune-plaintext.sh examples/configs-encrypted/raw

# Push encrypted example configs to etcd
example-push prefix="/myapp/config" endpoints="127.0.0.1:2379": build
    ./bin/cfgctl push examples/configs-encrypted --prefix {{prefix}} --endpoints {{endpoints}}

# Clean build artifacts
clean:
    rm -rf bin

