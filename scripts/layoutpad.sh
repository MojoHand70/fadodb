#!/usr/bin/env bash
# Code placement for benchmark runs, sourced by scripts/benchgate.sh and
# scripts/layoutsweep.sh (ADR-0030). A benchmark that runs in a few
# nanoseconds moves by 10% with the address the linker gives its function,
# so a run that wants the cost of the code rather than its placement builds
# the tree several times with a padding function of growing size ahead of
# every benchmarked package's code. Go lays a package's functions out in
# file order, 32 bytes apart at least, so a file sorting first in the
# package shifts every function after it by one 32-byte slot or more.
#
#   layoutpad_write TREE K     placement K of TREE: 0 is the tree as it is,
#                              K > 0 writes a_layoutpad.go with 2(K-1)
#                              unfoldable statements, about 28 bytes each
#   layoutpad_remove TREE      removes every a_layoutpad.go under TREE
#   layoutpad_packages TREE    lists "dir name" of the packages that
#                              declare benchmarks

layoutpad_packages() {
  (cd "$1" && go list -f '{{.Dir}} {{.Name}}' ./... 2>/dev/null) | while read -r dir name; do
    if grep -lq '^func Benchmark' "$dir"/*_test.go 2>/dev/null; then
      echo "$dir $name"
    fi
  done
}

layoutpad_remove() {
  find "$1" -name a_layoutpad.go -not -path '*/.git/*' -delete 2>/dev/null || true
}

layoutpad_write() {
  local tree=$1 k=$2 dir name i
  layoutpad_remove "$tree"
  if [ "$k" -eq 0 ]; then
    return 0
  fi
  layoutpad_packages "$tree" | while read -r dir name; do
    {
      echo "package $name"
      echo
      echo "// Padding written by scripts/layoutpad.sh for one benchmark run."
      echo "// It is removed when the run ends and must never be committed."
      echo "var layoutPadSink = layoutPad(1)"
      echo
      echo "//go:noinline"
      echo "func layoutPad(x int) int {"
      for i in $(seq 1 $((2 * (k - 1)))); do
        echo "	x ^= x >> $(((i % 60) + 1))"
        echo "	x += $i"
      done
      echo "	return x"
      echo "}"
    } > "$dir/a_layoutpad.go"
  done
}
