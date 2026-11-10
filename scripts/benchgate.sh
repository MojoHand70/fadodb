#!/usr/bin/env bash
# Benchmark regression gate. Compares the benchmarks in the working tree with
# a base git ref using benchstat and fails on: more than BENCH_TIME_PCT percent
# slower (sec/op), more than BENCH_BYTES_PCT percent more bytes per operation
# (B/op), or any increase in allocations per operation (allocs/op). Only
# statistically significant changes count; benchstat prints "~" for the rest.
#
# Code placement is measured out (ADR-0030). A benchmark that runs in a few
# nanoseconds moves by 10% with the address the linker gives its function,
# which the code under review does not decide. So both trees are built at
# BENCH_PLACEMENTS placements, the tree as it is and then shifted by one
# 32-byte slot or more each time (scripts/layoutpad.sh), with BENCH_COUNT
# rounds per placement interleaved between the trees so that a shared
# runner's drift lands on both alike. Per benchmark and per tree, the
# placement with the lowest median time is the one compared.
#
# The gate runs the benchmarks the budgets name, the hot paths of
# docs/architecture.md "Initial budgets" and of the records of each phase,
# not every benchmark in the tree: the VM's arithmetic, the recalculation,
# the lookups, the transition commit, the listing, the guard loop, the
# projection and the write check. The others run by hand and for a
# release's perf page. BENCH_PATTERN overrides the set; BENCH_PATTERN=.
# runs every benchmark of BENCH_PACKAGES.
#
# Usage:
#   scripts/benchgate.sh [base-ref]     default base-ref: origin/main
#   scripts/benchgate.sh --csv FILE     evaluate an existing benchstat CSV
#
# See docs/adr/0012-performance-gates.md and
# docs/adr/0030-benchmark-gate-measures-the-minimum-over-placements.md.
set -euo pipefail

TIME_PCT="${BENCH_TIME_PCT:-5}"
BYTES_PCT="${BENCH_BYTES_PCT:-5}"
COUNT="${BENCH_COUNT:-5}"
PLACEMENTS="${BENCH_PLACEMENTS:-5}"
PATTERN="${BENCH_PATTERN:-^Benchmark(VM_Arithmetic5|VM_Exact5|Recalculate|Recalculate50|LookupIndexed|LookupKeyedStatement|Transition|Transitions10|GuardLoop|Project|WriteCheck)$}"
PACKAGES="${BENCH_PACKAGES:-./internal/formula/ ./internal/store/}"

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

# best_placement reads the per-placement result files of one tree and
# prints, for each benchmark, the rounds of the placement with the lowest
# median time, under the configuration lines benchstat groups by.
best_placement() {
  awk -v placements="$PLACEMENTS" '
    FNR == 1 { file++; pkg = "" }
    /^(goos|goarch|cpu):/ { if (file == 1) print; next }
    /^pkg:/ { pkg = $0; next }
    /^Benchmark/ {
      key = pkg SUBSEP $1
      if (!(key in seen)) { seen[key] = 1; order[++nkeys] = key }
      n = ++count[key, file]
      value[key, file, n] = $3 + 0
      line[key, file, n] = $0
    }
    END {
      for (i = 1; i <= nkeys; i++) {
        key = order[i]
        best = 0
        for (f = 1; f <= file; f++) {
          n = count[key, f] + 0
          if (n == 0) continue
          for (a = 1; a <= n; a++) sorted[a] = value[key, f, a]
          for (a = 2; a <= n; a++) { v = sorted[a]; for (b = a - 1; b >= 1 && sorted[b] > v; b--) sorted[b + 1] = sorted[b]; sorted[b + 1] = v }
          med = sorted[int((n + 1) / 2)]
          if (best == 0 || med < bestmed) { best = f; bestmed = med }
        }
        if (best == 0) continue
        split(key, parts, SUBSEP)
        if (parts[1] != lastpkg) { print parts[1]; lastpkg = parts[1] }
        for (a = 1; a <= count[key, best]; a++) print line[key, best, a]
      }
    }
  ' "$@"
}

# spread prints the median time of every benchmark at every placement for
# one tree, so that a placement effect is visible in the log.
spread() {
  local label=$1
  shift
  awk -v label="$label" '
    FNR == 1 { file++ }
    /^Benchmark/ {
      if (!($1 in seen)) { seen[$1] = 1; order[++nkeys] = $1 }
      n = ++count[$1, file]
      value[$1, file, n] = $3 + 0
      unit = $4
    }
    END {
      for (i = 1; i <= nkeys; i++) {
        name = order[i]
        printf "benchgate: %-5s %-36s", label, name
        for (f = 1; f <= file; f++) {
          n = count[name, f] + 0
          for (a = 1; a <= n; a++) sorted[a] = value[name, f, a]
          for (a = 2; a <= n; a++) { v = sorted[a]; for (b = a - 1; b >= 1 && sorted[b] > v; b--) sorted[b + 1] = sorted[b]; sorted[b + 1] = v }
          printf " %10s", (n == 0) ? "-" : sorted[int((n + 1) / 2)]
        }
        printf " %s\n", unit
      }
    }
  ' "$@"
}

if [ "${1:-}" = "--csv" ]; then
  evaluate_csv "$2"
  exit $?
fi

cd "$(dirname "$0")/.."
# shellcheck source=scripts/layoutpad.sh
. scripts/layoutpad.sh
BASE="${1:-origin/main}"
work="$(mktemp -d)"
cleanup() {
  layoutpad_remove "$PWD"
  git worktree remove --force "$work/base" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

# One round of the gate's benchmarks in the tree at $1.
run_bench() {
  # shellcheck disable=SC2086
  (cd "$1" && go test -run '^$' -bench "$PATTERN" -benchmem -count 1 $PACKAGES 2>&1) || true
}

if find . -name a_layoutpad.go -not -path './.git/*' | grep -q .; then
  echo "benchgate: removing padding files left by an earlier run"
  layoutpad_remove "$PWD"
fi

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

# Every placement of both trees, the measured rounds alternating between
# the trees so that drift on a shared runner, thermal, frequency or a
# neighbour's load, lands on both alike instead of on whichever tree
# happened to run first.
for k in $(seq 0 $((PLACEMENTS - 1))); do
  layoutpad_write "$PWD" "$k"
  layoutpad_write "$work/base" "$k"
  : > "$work/new-$k.txt"
  : > "$work/old-$k.txt"
  for _ in $(seq "$COUNT"); do
    run_bench "$PWD" >> "$work/new-$k.txt"
    run_bench "$work/base" >> "$work/old-$k.txt"
  done
done
layoutpad_remove "$PWD"
layoutpad_remove "$work/base"

old_files=()
new_files=()
for k in $(seq 0 $((PLACEMENTS - 1))); do
  old_files+=("$work/old-$k.txt")
  new_files+=("$work/new-$k.txt")
done
echo "benchgate: median sec/op per placement (0 is the tree as it is)"
spread base "${old_files[@]}"
spread new "${new_files[@]}"
best_placement "${old_files[@]}" > "$work/old.txt"
best_placement "${new_files[@]}" > "$work/new.txt"

echo "benchgate: best placement of each tree, $COUNT rounds"
benchstat "$work/old.txt" "$work/new.txt" 2>/dev/null || true
benchstat -format csv "$work/old.txt" "$work/new.txt" > "$work/cmp.csv" 2>/dev/null || true

if evaluate_csv "$work/cmp.csv"; then
  echo "benchgate: ok (base $BASE, $PLACEMENTS placements, $COUNT rounds each, benchmarks $PATTERN; limits sec/op +${TIME_PCT}%, B/op +${BYTES_PCT}%, allocs/op +0)"
else
  echo "benchgate: failed, see the regressions above"
  exit 1
fi
