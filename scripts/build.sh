#!/bin/sh
# Produces bin/machine-manager, the binary every entrypoint in the manifest
# runs. herdr clones the repository and runs this during `herdr plugin install`.
#
# Building from source comes first: the result always matches the checkout.
# The release asset is the fallback for a machine without a Go toolchain, and
# is only used when its checksum matches the one recorded in this checkout.
set -eu

cd "$(dirname "$0")/.."
target=bin/machine-manager
mkdir -p bin

if command -v go >/dev/null 2>&1; then
	if go build -o "$target.tmp" ./cmd/machine-manager; then
		mv "$target.tmp" "$target"
		exit 0
	fi
	rm -f "$target.tmp"
	echo "build.sh: go build failed; falling back to the release binary" >&2
fi

version=$(sed -n 's/^version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' herdr-plugin.toml | head -1)
case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) echo "build.sh: no release binary for $(uname -s); install Go and retry" >&2; exit 1 ;;
esac
case "$(uname -m)" in
arm64 | aarch64) arch=arm64 ;;
x86_64 | amd64) arch=amd64 ;;
*) echo "build.sh: no release binary for $(uname -m); install Go and retry" >&2; exit 1 ;;
esac

asset="machine-manager-$os-$arch"
url="https://github.com/vika2603/herdr-machine-manager/releases/download/v$version/$asset"

expected=$(sed -n "s/^\([0-9a-f]\{64\}\)  $asset\$/\1/p" scripts/checksums.txt)
if [ -z "$expected" ]; then
	echo "build.sh: scripts/checksums.txt has no entry for $asset" >&2
	exit 1
fi

if ! curl -fsSL -o "$target.tmp" "$url"; then
	rm -f "$target.tmp"
	echo "build.sh: could not download $url" >&2
	exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$target.tmp" | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then
	actual=$(shasum -a 256 "$target.tmp" | cut -d' ' -f1)
else
	rm -f "$target.tmp"
	echo "build.sh: no sha256 tool to verify the download" >&2
	exit 1
fi

if [ "$actual" != "$expected" ]; then
	rm -f "$target.tmp"
	echo "build.sh: checksum mismatch for $asset" >&2
	exit 1
fi

chmod +x "$target.tmp"
mv "$target.tmp" "$target"
