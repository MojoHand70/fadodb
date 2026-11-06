#!/usr/bin/env bash
# Benchmark regression gate. Compares the benchmarks in the working tree with
# a base git ref using benchstat and fails on: more than BENCH_TIME_PCT percent
# slower (sec/op), more than BENCH_BYTES_PCT percent more bytes per operation
# (B/op), or any increase in allocations per operation (allocs/op). Only
# statistically significant changes count; benchstat prints "~" for the rest.
# Both trees get a warm-up round, then BENCH_COUNT rounds interleaved, so a
# shared runner's drift cannot be mistaken for a regression.
#
# Usage:
#   scripts/benchgate.sh [base-ref]     default base-ref: origin/main
#   scripts/benchgate.sh --csv FILE     evaluate an existing benchstat CSV
#
# See docs/adr/0012-performance-gates.md.
set -euo pipefail

TIME_PCT="${BENCH_TIME_PCT:-5}"
BYTES_PCT="${BENCH_BYTES_PCT:-5}"
COUNT="${BENCH_COUNT:-6}"

# evaluate_csv prints every regression found in a benchstat CSV and returns 1
# when there is at least one.
evaluate_csv() {
  awk -F, -v time_pct="$TIME_PCT" -v bytes_pct="$BYTES_PCT" '
    $1 == "" && ($2 == "sec/op" || $2 == "B/op" || $2 == "allocs/op") { unit = $2; next }
    $1 == "" || $1 == "geomean" { next }
    {
      delta = $6
      # benchstat cannot express a change from zero as a percentage. Treat a
      # measured increase from a zero baseline as a regression (0 -> 1 allocs).
      if ((delta == "?" || delta ~ /∞/) && $2 != "" && ($4 + 0) > ($2 + 0)) {
        printf "benchgate: %s %s went from %s to %s\n", $1, unit, $2, $4
        bad = 1
        next
      }
      if (delta !~ /^\+/) next
      pct = substr(delta, 2)
      sub(/%$/, "", pct)
      pct += 0
      limit = (unit == "sec/op") ? time_pct : (unit == "B/op") ? bytes_pct : 0
      if (pct > limit) {
        printf "benchgate: %s %s regressed by %s (limit +%s%%)\n", $1, unit, delta, limit
        bad = 1
      }
    }
    END { exit bad ? 1 : 0 }
  ' "$1"
}

if [ "${1:-}" = "--csv" ]; then
  evaluate_csv "$2"
  exit $?
fi

cd "$(dirname "$0")/.."
BASE="${1:-origin/main}"
work="$(mktemp -d)"
cleanup() {
  git worktree remove --force "$work/base" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

# One round of every benchmark in the tree at $1.
run_bench() {
  (cd "$1" && go test -run '^$' -bench . -benchmem -count 1 ./... 2>&1) || true
}

git worktree add --quiet --detach "$work/base" "$BASE"

# A warm-up round per tree compiles the test binaries and absorbs the
# frequency and cache effects of that compile, and tells whether there is
# anything to compare at all.
run_bench "$PWD" > "$work/warm-new.txt"
if ! grep -q '^Benchmark' "$work/warm-new.txt"; then
  echo "benchgate: no benchmarks in the working tree, nothing to compare"
  exit 0
fi
run_bench "$work/base" > "$work/warm-old.txt"
if ! grep -q '^Benchmark' "$work/warm-old.txt"; then
  echo "benchgate: no benchmarks at $BASE, nothing to compare"
  exit 0
fi

# The measured rounds alternate between the two trees, so that drift on a
# shared runner, thermal, frequency or a neighbour's load, lands on both
# alike instead of on whichever tree happened to run first.
: > "$work/new.txt"
: > "$work/old.txt"
for _ in $(seq "$COUNT"); do
  run_bench "$PWD" >> "$work/new.txt"
  run_bench "$work/base" >> "$work/old.txt"
done

benchstat "$work/old.txt" "$work/new.txt" 2>/dev/null || true
benchstat -format csv "$work/old.txt" "$work/new.txt" > "$work/cmp.csv" 2>/dev/null || true

if evaluate_csv "$work/cmp.csv"; then
  echo "benchgate: ok (base $BASE, $COUNT runs; limits sec/op +${TIME_PCT}%, B/op +${BYTES_PCT}%, allocs/op +0)"
else
  echo "benchgate: failed, see the regressions above"
  exit 1
fi
