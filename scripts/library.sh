#!/usr/bin/env bash
# The library of C, libfadodb-core (ADR-0101, step D): built, tried and
# packed on the system it is for. A library of C is built with that
# system's C compiler and cannot be built for another system from here.
# A workflow runs this on a machine of each system
# (.github/workflows/library.yml); a person runs it on their own.
#
#   scripts/library.sh build [VERSION]   bin/library/libfadodb-core.so (.dylib, fadodb-core.dll) and fadodb.h
#   scripts/library.sh try               the program of C and the Python client against what was built
#   scripts/library.sh pack [VERSION]    bin/library/fadodb-core-library_VERSION_OS_ARCH.tar.gz, or .zip,
#                                        and the wheel of the Python package for this system beside it
#
# VERSION is a tag or a version, v0.9.0 or 0.9.0; dev when it is left out.
set -euo pipefail
cd "$(dirname "$0")/.."

out=bin/library
version="${2:-dev}"
version="${version#v}"
os="$(go env GOOS)"
arch="$(go env GOARCH)"
case "$os" in
windows) name=fadodb-core.dll ;;
darwin) name=libfadodb-core.dylib ;;
*) name=libfadodb-core.so ;;
esac
library="$out/$name"

# LIBRARY_GLIBC_MAX is the newest C library a library for Linux may
# need, which whoever builds a release says, as PROGRAM_GLIBC_MAX of
# scripts/program.sh says it of the program.
GLIBC_MAX="${LIBRARY_GLIBC_MAX:-}"

# python is the machine's own Python; the client needs nothing else. On
# Windows python3 may be the stub of the store.
if [ "$os" = "windows" ]; then
  python="$(command -v python || command -v python3 || true)"
else
  python="$(command -v python3 || command -v python || true)"
fi

# native is a path as the programs of the system take it: on Windows,
# under Git's bash, /d/a/x is D:\a\x to Go and to Python.
native() {
  if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else printf '%s\n' "$1"; fi
}

case "${1:-}" in
build)
  mkdir -p "$out"
  commit="$(git rev-parse --short HEAD 2>/dev/null || echo "")"
  date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  (cd cabi && CGO_ENABLED=1 go build -buildmode=c-shared -trimpath \
    -ldflags "-s -w -X main.version=$version -X main.commit=$commit -X main.date=$date" \
    -o "../$library" .)
  # Go writes a header of its own beside the library; fadodb.h is the
  # one for people, and a test holds the two to the same functions.
  rm -f "${library%.*}.h"
  cp cabi/fadodb.h "$out/fadodb.h"
  ls -l "$library"
  ;;
try)
  [ -f "$library" ] || { echo "library: $library is not built; run scripts/library.sh build"; exit 1; }
  if [ "$os" = "linux" ] && [ -n "$GLIBC_MAX" ]; then
    needs="$(objdump -T "$library" | grep -o -E 'GLIBC_[0-9.]+' | sed 's/GLIBC_//' | sort -V | tail -1)"
    if [ "$(printf '%s\n%s\n' "$needs" "$GLIBC_MAX" | sort -V | tail -1)" != "$GLIBC_MAX" ]; then
      echo "library: it needs the C library $needs, and it may need $GLIBC_MAX at the most"
      echo "  it was built on a newer Linux than a release is built on"
      exit 1
    fi
    echo "library: it needs the C library $needs or newer"
  fi
  if [ "$os" = "windows" ] && command -v objdump >/dev/null 2>&1; then
    # A library that needs the libraries of the compiler it was built
    # with loads on the machine that built it and on few others.
    borrowed="$(objdump -p "$library" | grep -i -E 'DLL Name: (libstdc\+\+|libgcc|libwinpthread)' || true)"
    if [ -n "$borrowed" ]; then
      echo "library: it needs libraries of the compiler, which a user's machine does not have:"
      echo "$borrowed"
      exit 1
    fi
  fi
  given="$(native "$(pwd)/$library")"
  # The program of C, compiled against fadodb.h and linked to this very
  # file (cabi/library_c_test.go).
  (cd cabi && FADODB_LIBRARY="$given" FADODB_CABI=must CGO_ENABLED=1 \
    go test -count=1 -run '^TestTheLibraryFromC$' .)
  # The Python client in the same process, with this very file.
  [ -n "$python" ] || { echo "library: no Python to try the client with"; exit 1; }
  (cd clients/py/tests && FADODB_LIBRARY="$given" FADODB_PYTHON=must "$python" -m unittest test_both_ways.InTheSameProcess)
  ;;
pack)
  [ -f "$library" ] || { echo "library: $library is not built; run scripts/library.sh build"; exit 1; }
  pack="fadodb-core-library_${version}_${os}_${arch}"
  stage="$out/$pack"
  rm -rf "$stage"
  mkdir -p "$stage"
  cp "$library" "$out/fadodb.h" LICENSE "$stage/"
  cp cabi/README.md "$stage/README.md"
  if [ "$os" = "windows" ]; then
    rm -f "$out/$pack.zip"
    (cd "$stage" && 7z a -tzip "../$pack.zip" . >/dev/null)
    echo "$out/$pack.zip"
  else
    tar -C "$stage" -czf "$out/$pack.tar.gz" .
    echo "$out/$pack.tar.gz"
  fi
  rm -rf "$stage"
  # The Python package for this system, with the library in it, as a
  # wheel; and, on Linux for Intel, the one for every system, without
  # it, for a program that only talks to a server.
  [ -n "$python" ] || { echo "library: no Python to make the wheel with"; exit 1; }
  case "$os/$arch" in
  linux/*)
    needs="$(objdump -T "$library" | grep -o -E 'GLIBC_[0-9.]+' | sed 's/GLIBC_//' | sort -V | tail -1)"
    machine=x86_64
    [ "$arch" = "arm64" ] && machine=aarch64
    platform="manylinux_${needs//./_}_$machine"
    ;;
  darwin/arm64) platform=macosx_12_0_arm64 ;;
  darwin/amd64) platform=macosx_12_0_x86_64 ;;
  windows/amd64) platform=win_amd64 ;;
  *) echo "library: no wheel is made for $os/$arch"; exit 1 ;;
  esac
  wheel="$("$python" clients/py/wheel.py --version "$version" --out "$out" --library "$library" --platform "$platform")"
  echo "$wheel"
  if [ "$os/$arch" = "linux/amd64" ]; then
    "$python" clients/py/wheel.py --version "$version" --out "$out"
  fi
  # The wheel is tried as a user has it: installed by pip, or unpacked
  # where there is no pip, with no library named, so that fadodb.open
  # finds the one inside.
  installed="$(native "$(mktemp -d)")"
  if "$python" -m pip --version >/dev/null 2>&1; then
    "$python" -m pip install --quiet --no-deps --no-index --target "$installed" "$wheel"
    echo "library: pip installed $wheel"
  else
    "$python" -c 'import sys, zipfile; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])' "$wheel" "$installed"
    echo "library: no pip here: $wheel unpacked"
  fi
  (cd clients/py/tests && env -u FADODB_LIBRARY FADODB_PYTHON_PACKAGE="$installed" FADODB_PYTHON=must \
    "$python" -m unittest test_both_ways.InTheSameProcess)
  rm -rf "$installed"
  ;;
*)
  sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
