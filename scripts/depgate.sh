#!/usr/bin/env bash
# Dependency budget gate: every direct module in go.mod must be listed in
# docs/dependencies.md with a justification, and the core binary links no
# HTTP client.
# See docs/adr/0002-pure-go-core-and-dependency-budget.md and
# docs/adr/0045-actions-are-an-extension-point.md, item 7.
set -euo pipefail
cd "$(dirname "$0")/.."

missing=0
while read -r mod; do
  [ -z "$mod" ] && continue
  if ! grep -qF -- "$mod" docs/dependencies.md; then
    echo "depgate: $mod is not listed in docs/dependencies.md"
    missing=1
  fi
done < <(go list -m -f '{{if and (not .Indirect) (not .Main)}}{{.Path}}{{end}}' all)

# The core links no HTTP client (ADR-0045, item 7): a program that does not
# import fado/outbound cannot send a request. The linker keeps only what is
# reachable, so the transport's round trip is in the binary exactly when
# something can call it.
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT
CGO_ENABLED=0 go build -o "$out/fadodb" ./cmd/fadodb
if go tool nm "$out/fadodb" | grep -q 'net/http\.(\*Transport)\.roundTrip'; then
  echo "depgate: the core binary links an HTTP client; only fado/outbound may (ADR-0045)"
  missing=1
fi
if go list -deps ./cmd/fadodb ./fado | grep -q '/fado/outbound$'; then
  echo "depgate: the core imports fado/outbound; only a host program may (ADR-0045)"
  missing=1
fi

if [ "$missing" -eq 0 ]; then
  echo "depgate: ok"
fi
exit "$missing"
