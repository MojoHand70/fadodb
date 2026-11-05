#!/usr/bin/env bash
# Dependency budget gate: every direct module in go.mod must be listed in
# docs/dependencies.md with a justification.
# See docs/adr/0002-pure-go-core-and-dependency-budget.md.
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

if [ "$missing" -eq 0 ]; then
  echo "depgate: ok"
fi
exit "$missing"
