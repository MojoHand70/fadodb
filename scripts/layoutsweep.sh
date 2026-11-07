#!/usr/bin/env bash
# Layout sweep for the benchmarks of one package: the spread of each
# benchmark over code placements (ADR-0030). The tree is built at N
# placements with scripts/layoutpad.sh, the tree as it is and then shifted
# by one 32-byte slot or more each time, and the chosen benchmarks run once
# per placement. A placement effect shows as two or more modes across the
# sweep; a change in the cost of the code moves every placement.
#
# Usage:
#   scripts/layoutsweep.sh [-p package] [-n placements] [bench-regex]
#
# Defaults: package ./internal/formula, 8 placements, benchmarks matching
# VM_. The padding files are removed when the sweep ends, however it ends.
set -euo pipefail

pkg=./internal/formula
steps=8
while getopts "p:n:" opt; do
  case $opt in
    p) pkg=$OPTARG ;;
    n) steps=$OPTARG ;;
    *) echo "usage: scripts/layoutsweep.sh [-p package] [-n placements] [bench-regex]" >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
regex="${1:-VM_}"

cd "$(dirname "$0")/.."
# shellcheck source=scripts/layoutpad.sh
. scripts/layoutpad.sh
work="$(mktemp -d)"
cleanup() {
  layoutpad_remove "$PWD"
  rm -rf "$work"
}
trap cleanup EXIT

: > "$work/all.txt"
for k in $(seq 0 $((steps - 1))); do
  layoutpad_write "$PWD" "$k"
  go test -c -o "$work/sweep.test" "$pkg"
  "$work/sweep.test" -test.run '^$' -test.bench "$regex" -test.benchmem 2>&1 \
    | awk -v s="$k" '/^Benchmark/ { printf "placement %d  %-40s %10s ns/op\n", s, $1, $3 }' \
    | tee -a "$work/all.txt"
done
layoutpad_remove "$PWD"

echo
echo "benchmark: min / median / max over $steps placements"
awk '{ print $3, $4 }' "$work/all.txt" | sort -k1,1 -k2,2n | awk '
  $1 != name { if (name != "") report(); name = $1; n = 0 }
  { v[++n] = $2 }
  END { report() }
  function report() {
    printf "%-40s %10s %10s %10s ns/op\n", name, v[1], v[int((n + 1) / 2)], v[n]
  }'
