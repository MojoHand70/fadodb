#!/usr/bin/env bash
# The program with the analytical engine, fadodb (ADR-0089, step 6):
# built, tried and packed on the system it is for. DuckDB is C++, so the
# program needs that system's C compiler and cannot be built for another
# system from here, as fadodb-core, in pure Go, is. A workflow runs this
# on a machine of each system (.github/workflows/programs.yml); a person
# runs it on their own.
#
#   scripts/program.sh build [VERSION]   bin/programs/fadodb, or fadodb.exe
#   scripts/program.sh try               play the program's story with what was built
#   scripts/program.sh pack [VERSION]    bin/programs/fadodb_VERSION_OS_ARCH.tar.gz, or .zip
#
# VERSION is a tag or a version, v0.8.0 or 0.8.0; dev when it is left out.
set -euo pipefail
cd "$(dirname "$0")/.."

out=bin/programs
version="${2:-dev}"
version="${version#v}"
os="$(go env GOOS)"
arch="$(go env GOARCH)"
exe=""
[ "$os" = "windows" ] && exe=".exe"
program="$out/fadodb$exe"

# PROGRAM_GLIBC_MAX is the newest C library a Linux program may need,
# which whoever builds a release says: 2.35, that of Ubuntu 22.04, the
# oldest Linux a release is built on. A program that needs a newer one
# starts on the Linux it was built on and on no older, so a builder that
# was moved to a newer Linux is told here and not by a user. A person
# who builds for their own machine says nothing, and nothing is asked.
GLIBC_MAX="${PROGRAM_GLIBC_MAX:-}"

case "${1:-}" in
build)
  mkdir -p "$out"
  commit="$(git rev-parse --short HEAD 2>/dev/null || echo "")"
  date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  (cd analytics && CGO_ENABLED=1 go build -trimpath \
    -ldflags "-s -w -X main.version=$version -X main.commit=$commit -X main.date=$date" \
    -o "../$program" ./cmd/fadodb)
  "$program" version
  ;;
try)
  [ -f "$program" ] || { echo "program: $program is not built; run scripts/program.sh build"; exit 1; }
  said="$("$program" version)"
  case "$said" in
  fadodb\ *"with the analytical engine") ;;
  *) echo "program: it says of itself: $said"; exit 1 ;;
  esac
  if [ "$os" = "linux" ] && [ -n "$GLIBC_MAX" ]; then
    needs="$(objdump -T "$program" | grep -o -E 'GLIBC_[0-9.]+' | sed 's/GLIBC_//' | sort -V | tail -1)"
    if [ "$(printf '%s\n%s\n' "$needs" "$GLIBC_MAX" | sort -V | tail -1)" != "$GLIBC_MAX" ]; then
      echo "program: it needs the C library $needs, and it may need $GLIBC_MAX at the most"
      echo "  it was built on a newer Linux than a release is built on"
      exit 1
    fi
    echo "program: it needs the C library $needs or newer"
  fi
  if [ "$os" = "windows" ] && command -v objdump >/dev/null 2>&1; then
    # A program that needs the libraries of the compiler it was built
    # with starts on the machine that built it and on few others.
    borrowed="$(objdump -p "$program" | grep -i -E 'DLL Name: (libstdc\+\+|libgcc|libwinpthread)' || true)"
    if [ -n "$borrowed" ]; then
      echo "program: it needs libraries of the compiler, which a user's machine does not have:"
      echo "$borrowed"
      exit 1
    fi
  fi
  # The story an operator plays: a database made, a user and a key, the
  # server started, a class, documents, and the flow of the class, which
  # the copy answers (analytics/cmd/fadodb/program_test.go).
  (cd analytics && FADODB_PROGRAM="$(cd .. && pwd)/$program" CGO_ENABLED=1 go test -count=1 -run '^TestTheProgramWithTheEngine$' ./cmd/fadodb/)
  ;;
pack)
  [ -f "$program" ] || { echo "program: $program is not built; run scripts/program.sh build"; exit 1; }
  name="fadodb_${version}_${os}_${arch}"
  stage="$out/$name"
  rm -rf "$stage"
  mkdir -p "$stage"
  cp "$program" LICENSE README.md "$stage/"
  # DuckDB is linked into the program, and its notice (MIT) travels with
  # it: the Go driver carries the same words as DuckDB's own licence.
  cp "$(cd analytics && go list -m -f '{{.Dir}}' github.com/duckdb/duckdb-go-bindings)/LICENSE" "$stage/LICENSE-duckdb"
  chmod 644 "$stage/LICENSE-duckdb"
  if [ "$os" = "windows" ]; then
    rm -f "$out/$name.zip"
    (cd "$stage" && 7z a -tzip "../$name.zip" . >/dev/null)
    echo "$out/$name.zip"
  else
    tar -C "$stage" -czf "$out/$name.tar.gz" .
    echo "$out/$name.tar.gz"
  fi
  rm -rf "$stage"
  ;;
*)
  sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
