#!/usr/bin/env bash
# Binary size gate: a release-style build of the core binary must stay under
# the budget in docs/adr/0012-performance-gates.md.
set -euo pipefail
cd "$(dirname "$0")/.."

LIMIT_MB="${SIZE_LIMIT_MB:-15}"
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$out/fadodb" ./cmd/fadodb
bytes="$(stat -c %s "$out/fadodb" 2>/dev/null || stat -f %z "$out/fadodb")"
limit=$((LIMIT_MB * 1024 * 1024))

printf 'sizegate: core binary is %d bytes, budget %d MB\n' "$bytes" "$LIMIT_MB"
if [ "$bytes" -gt "$limit" ]; then
  echo "sizegate: over budget"
  exit 1
fi
