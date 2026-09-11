set shell := ["zsh", "-cu"]

plugin_root := justfile_directory()
state_dir := env("XDG_STATE_HOME", env("HOME") / ".local/state") / "herdr/plugins/herdr.machine-manager"

default: check

# Build the plugin binary.
build:
    # Through a temporary name: `go build -o` truncates its target first, and a
    # keypress during that window finds no binary and fails silently.
    go build -o bin/machine-manager.new ./cmd/machine-manager
    mv bin/machine-manager.new bin/machine-manager

test:
    go test -race ./...

lint:
    go vet ./...
    golangci-lint run ./...

fmt:
    gofmt -w cmd internal

cover:
    go test -cover ./internal/...

check: build test lint

# Point herdr at this working tree.
link: build
    # A rebuild is picked up on the next open: the running daemon retires when
    # a newer build asks it to.
    herdr plugin link {{plugin_root}}

unlink:
    herdr plugin unlink herdr.machine-manager

# Open the popup without pressing the key.
open: build
    herdr plugin action invoke open --plugin herdr.machine-manager

# Exit codes, stdout and stderr of every plugin command herdr ran.
logs:
    herdr plugin log list

# What the resident daemon wrote. Empty is the healthy case.
daemon-log:
    tail -n 40 {{state_dir}}/daemon.log

# The connections the plugin manages.
connections:
    cat {{state_dir}}/connections.json

# Cross-compile the release binaries and record the checksums build.sh checks.
dist:
    rm -rf dist && mkdir -p dist
    for target in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do \
        CGO_ENABLED=0 GOOS=${target%-*} GOARCH=${target#*-} \
            go build -trimpath -o dist/machine-manager-$target ./cmd/machine-manager; \
    done
    cd dist && shasum -a 256 machine-manager-* > ../scripts/checksums.txt
