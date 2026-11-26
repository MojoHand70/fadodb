#!/usr/bin/env bash
# Dependency budget gate: every direct module in go.mod must be listed in
# docs/dependencies.md with a justification, and a program that does not
# allow outbound calls links no HTTP client.
# See docs/adr/0002-pure-go-core-and-dependency-budget.md,
# docs/adr/0045-actions-are-an-extension-point.md, item 7, and
# docs/adr/0059-fadodb-serve.md, item 4, and
# docs/adr/0072-the-size-budget-of-the-core-program.md, item 3.
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

# A program links an HTTP client only when it allows outbound calls
# (ADR-0045, item 7): the engine and the door never import fado/outbound,
# which makes the one client of the module. It is checked on
# examples/server, a program with the engine and the door and nothing
# else. The linker keeps only what is reachable, so the transport's round
# trip is in a binary exactly when something can call it. The fadodb
# tool is a host program that allows outbound calls when its settings
# list a host (ADR-0059, item 4), so it imports the package.
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT
# The symbols go to a file first: a search that ends at the first match
# would cut the pipe of the tool that lists them.
round_trip='net/http\.(\*Transport)\.roundTrip'
CGO_ENABLED=0 go build -o "$out/server" ./examples/server
go tool nm "$out/server" > "$out/server.symbols"
if grep -q "$round_trip" "$out/server.symbols"; then
  echo "depgate: a program with the engine and the door links an HTTP client; only fado/outbound may bring one (ADR-0045)"
  missing=1
fi
if go list -deps ./fado ./fado/httpdoor ./examples/server | grep -q '/fado/outbound$'; then
  echo "depgate: the engine or the door imports fado/outbound; only a host program may (ADR-0045)"
  missing=1
fi
# And the tool does link it: a gate that looked for the wrong symbol
# would pass for ever.
CGO_ENABLED=0 go build -o "$out/fadodb" ./cmd/fadodb-core
go tool nm "$out/fadodb" > "$out/fadodb.symbols"
if ! grep -q "$round_trip" "$out/fadodb.symbols"; then
  echo "depgate: the fadodb tool links no HTTP client, so this gate no longer sees one where there is one"
  missing=1
fi

# No package that calls methods by their names (ADR-0072, item 3): a
# template keeps every method of the program, which made it 2 MB larger.
if go list -deps ./cmd/fadodb-core | grep -qE '^(text|html)/template$'; then
  echo "depgate: the core program imports text/template or html/template, which keep every method of the program (ADR-0072)"
  missing=1
fi

if [ "$missing" -eq 0 ]; then
  echo "depgate: ok"
fi
exit "$missing"
