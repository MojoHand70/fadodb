#!/usr/bin/env bash
# Layout sweep for the benchmarks of one package. The same machine code
# runs at a different speed depending on where the linker places it: the
# 13 ns VM loop moves by 10% between two placements 32 bytes apart, on the
# development machine and on the CI runner alike, because the function
# start alternates between the two halves of a 64-byte line. The sweep
# rebuilds the test binary with a padding function of growing size ahead
# of the package's other code, which shifts every later function, and runs
# the chosen benchmarks once per placement. A placement effect shows as two
# or more modes across the sweep; a regression moves every placement.
#
# Usage:
#   scripts/layoutsweep.sh [-p package] [-n placements] [bench-regex]
#
# Defaults: package ./internal/formula, 8 placements, benchmarks matching
# VM_. The padding file is removed when the sweep ends, however it ends.
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
dir="$(go list -f '{{.Dir}}' "$pkg")"
name="$(go list -f '{{.Name}}' "$pkg")"
pad="$dir/a_layoutsweep_pad.go"
work="$(mktemp -d)"
cleanup() {
  rm -f "$pad"
  rm -rf "$work"
}
trap cleanup EXIT

# The padding function is kept by the linker because an init calls it,
# and its shifts and xors cannot be folded, so each step adds about 40
# bytes of code: one or two 32-byte function slots.
write_pad() {
  {
    echo "package $name"
    echo
    echo "// Padding for scripts/layoutsweep.sh; never committed."
    echo "var layoutSweepSink = layoutSweepPad(1)"
    echo
    echo "//go:noinline"
    echo "func layoutSweepPad(x int) int {"
    for i in $(seq 1 $(($1 * 4))); do
      echo "	x ^= x >> $(((i % 60) + 1))"
      echo "	x += $i"
    done
    echo "	return x"
    echo "}"
  } > "$pad"
}

: > "$work/all.txt"
for step in $(seq 0 $((steps - 1))); do
  write_pad "$step"
  go test -c -o "$work/sweep.test" "$pkg"
  "$work/sweep.test" -test.run '^$' -test.bench "$regex" -test.benchmem 2>&1 \
    | awk -v s="$step" '/^Benchmark/ { printf "placement %d  %-40s %10s ns/op\n", s, $1, $3 }' \
    | tee -a "$work/all.txt"
done

echo
echo "benchmark: min / median / max over $steps placements"
awk '{ print $3, $4 }' "$work/all.txt" | sort -k1,1 -k2,2n | awk '
  $1 != name { if (name != "") report(); name = $1; n = 0 }
  { v[++n] = $2 }
  END { report() }
  function report() {
    printf "%-40s %10s %10s %10s ns/op\n", name, v[1], v[int((n + 1) / 2)], v[n]
  }'
