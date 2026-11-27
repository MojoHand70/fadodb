#!/usr/bin/env bash
# The libraries of C (ADR-0101, step D; ADR-0102, step G): libfadodb-core,
# the engine of fadodb-core, and libfadodb, the engine of fadodb, with
# DuckDB's own shared library beside it. Built, tried and packed on the
# system they are for: a library of C is built with that system's C
# compiler and cannot be built for another system from here. A workflow
# runs this on a machine of each system (.github/workflows/library.yml);
# a person runs it on their own.
#
#   scripts/library.sh build [VERSION]   both, in bin/library, with fadodb.h; DuckDB's library is
#                                        fetched from DuckDB's release and held to the checksum the
#                                        Go driver records
#   scripts/library.sh try               the program of C and the Python client against each
#   scripts/library.sh pack [VERSION]    bin/library/fadodb-core-library_VERSION_OS_ARCH.tar.gz and
#                                        fadodb-library_…, .zip on Windows, and the wheel of the Python
#                                        package for this system, with libfadodb inside
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
windows) core=fadodb-core.dll full=fadodb.dll duck=duckdb.dll ;;
darwin) core=libfadodb-core.dylib full=libfadodb.dylib duck=libduckdb.dylib ;;
*) core=libfadodb-core.so full=libfadodb.so duck=libduckdb.so ;;
esac

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
# under Git's bash, /d/a/x is D:/a/x to Go, Python and the C compiler.
native() {
  if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s\n' "$1"; fi
}

# fetch takes DuckDB's shared library for this system from DuckDB's
# release, the one the Go driver is built from, and refuses a file
# whose checksum is not the one the driver records (ADR-0102, item 7).
fetch() {
  [ -n "$python" ] || { echo "library: no Python to read the Go driver's record of DuckDB with"; exit 1; }
  local bindings told url sum got
  # The driver is fetched first: go list names the folder of a module
  # only once the module is on the machine.
  bindings="$(cd cabi && go mod download github.com/duckdb/duckdb-go-bindings && go list -m -f '{{.Dir}}' github.com/duckdb/duckdb-go-bindings)"
  [ -n "$bindings" ] && [ -f "$bindings/duckdb-release.json" ] || { echo "library: Go names no folder of DuckDB's Go driver with its record of DuckDB: $bindings"; exit 1; }
  told="$("$python" - "$bindings/duckdb-release.json" "$os-$arch" <<'PY'
import json, sys
release = json.load(open(sys.argv[1]))
system = release["platforms"].get(sys.argv[2])
if system is None:
    sys.exit("DuckDB has no shared library for " + sys.argv[2])
print(release["base_url"] + "/duckdb-shared-libs-" + system["shared"] + ".tar.gz", system["shared_sha256"])
PY
)"
  url="${told% *}"
  sum="${told#* }"
  rm -rf "${out:?}/duckdb"
  mkdir -p "$out/duckdb"
  curl -fsSL --retry 3 -o "$out/duckdb.tar.gz" "$url"
  if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$out/duckdb.tar.gz" | cut -d' ' -f1)"; else got="$(shasum -a 256 "$out/duckdb.tar.gz" | cut -d' ' -f1)"; fi
  if [ "$got" != "$sum" ]; then
    echo "library: $url has the checksum $got, and the Go driver records $sum"
    exit 1
  fi
  tar -C "$out/duckdb" -xzf "$out/duckdb.tar.gz"
  rm -f "${out:?}/duckdb.tar.gz"
  cp "$out/duckdb/$duck" "$out/$duck"
  # DuckDB's notice travels with DuckDB (MIT): the Go driver carries the
  # same words as DuckDB's own licence.
  cp "$bindings/LICENSE" "$out/LICENSE-duckdb"
  chmod 644 "$out/LICENSE-duckdb"
  echo "library: DuckDB's $duck from $url"
}

# needed says what a library needs from its system, so that a log says
# which file a program that would not start was missing.
needed() {
  case "$os" in
  linux) readelf -d "$1" | grep NEEDED || true ;;
  darwin) otool -L "$1" ;;
  windows) objdump -p "$1" | grep -i 'DLL Name' || true ;;
  esac
}

# checked holds a built library to what a user's machine has.
checked() {
  local library="$1" needs borrowed
  echo "library: $library needs:"
  needed "$library" | sed 's/^/  /'
  if [ "$os" = "linux" ] && [ -n "$GLIBC_MAX" ]; then
    needs="$(objdump -T "$library" | grep -o -E 'GLIBC_[0-9.]+' | sed 's/GLIBC_//' | sort -V | tail -1)"
    if [ "$(printf '%s\n%s\n' "$needs" "$GLIBC_MAX" | sort -V | tail -1)" != "$GLIBC_MAX" ]; then
      echo "library: $library needs the C library $needs, and it may need $GLIBC_MAX at the most"
      echo "  it was built on a newer Linux than a release is built on"
      exit 1
    fi
    echo "library: $library needs the C library $needs or newer"
  fi
  if [ "$os" = "windows" ] && command -v objdump >/dev/null 2>&1; then
    # A library that needs the libraries of the compiler it was built
    # with loads on the machine that built it and on few others.
    borrowed="$(objdump -p "$library" | grep -i -E 'DLL Name: (libstdc\+\+|libgcc|libwinpthread)' || true)"
    if [ -n "$borrowed" ]; then
      echo "library: $library needs libraries of the compiler, which a user's machine does not have:"
      echo "$borrowed"
      exit 1
    fi
  fi
}

# tried runs the program of C and the Python client in the same process
# against a library, the very file built.
tried() {
  local given
  given="$(native "$(pwd)/$1")"
  (cd cabi && FADODB_LIBRARY="$given" FADODB_CABI=must CGO_ENABLED=1 \
    go test -count=1 -run '^TestTheLibraryFromC$' .)
  [ -n "$python" ] || { echo "library: no Python to try the client with"; exit 1; }
  (cd clients/py/tests && FADODB_LIBRARY="$given" FADODB_PYTHON=must FADODB_ENGINE="${2:-}" \
    "$python" -m unittest test_both_ways.InTheSameProcess)
}

# wheeled makes the wheel of the Python package for this system, with
# libfadodb and DuckDB's library inside, and tries it as a user has it.
wheeled() {
  local platform needs machine wheel installed
  case "$os/$arch" in
  linux/*)
    needs="$(objdump -T "$out/$full" "$out/$duck" 2>/dev/null | grep -o -E 'GLIBC_[0-9.]+' | sed 's/GLIBC_//' | sort -V | tail -1)"
    machine=x86_64
    [ "$arch" = "arm64" ] && machine=aarch64
    platform="manylinux_${needs//./_}_$machine"
    ;;
  darwin/arm64) platform=macosx_12_0_arm64 ;;
  darwin/amd64) platform=macosx_12_0_x86_64 ;;
  windows/amd64) platform=win_amd64 ;;
  *) echo "library: no wheel is made for $os/$arch"; exit 1 ;;
  esac
  wheel="$("$python" clients/py/wheel.py --version "$version" --out "$out" --platform "$platform" \
    --library "$out/$full" --library "$out/$duck" --license "$out/LICENSE-duckdb")"
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
    chmod +x "$installed"/fadodb/lib/* 2>/dev/null || true
    echo "library: no pip here: $wheel unpacked"
  fi
  (cd clients/py/tests && env -u FADODB_LIBRARY FADODB_PYTHON_PACKAGE="$installed" FADODB_PYTHON=must FADODB_ENGINE=must \
    "$python" -m unittest test_both_ways.InTheSameProcess)
  rm -rf "${installed:?}"
}

# packed makes the archive of one library, with what travels with it.
packed() {
  local pack="$1" stage
  shift
  stage="$out/$pack"
  rm -rf "${stage:?}"
  mkdir -p "$stage"
  cp "$@" "$out/fadodb.h" LICENSE THIRD_PARTY_NOTICES.md "$stage/"
  cp cabi/README.md "$stage/README.md"
  if [ "$os" = "windows" ]; then
    rm -f "${out:?}/${pack:?}.zip"
    (cd "$stage" && 7z a -tzip "../$pack.zip" . >/dev/null)
    echo "$out/$pack.zip"
  else
    tar -C "$stage" -czf "$out/$pack.tar.gz" .
    echo "$out/$pack.tar.gz"
  fi
  rm -rf "${stage:?}"
}

case "${1:-}" in
build)
  mkdir -p "$out"
  commit="$(git rev-parse --short HEAD 2>/dev/null || echo "")"
  date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  ldflags="-s -w -X main.version=$version -X main.commit=$commit -X main.date=$date"
  # On macOS a library's header keeps room for the name it is given
  # below.
  pad=''
  [ "$os" = "darwin" ] && pad='-Wl,-headerpad_max_install_names'
  (cd cabi && CGO_ENABLED=1 CGO_LDFLAGS="$pad" go build -buildmode=c-shared -trimpath -ldflags "$ldflags" -o "../$out/$core" .)
  # The library with DuckDB, linked to DuckDB's own library, which it
  # finds in its own folder.
  fetch
  case "$os" in
  linux) rpath='-Wl,-rpath,$ORIGIN' ;;
  darwin) rpath='-Wl,-rpath,@loader_path -Wl,-headerpad_max_install_names' ;;
  *) rpath='' ;;
  esac
  duckdb_dir="$(native "$(pwd)/$out/duckdb")"
  (cd cabi && CGO_ENABLED=1 CGO_LDFLAGS="-L$duckdb_dir $rpath" \
    go build -tags duckdb,duckdb_use_lib -buildmode=c-shared -trimpath -ldflags "$ldflags" -o "../$out/$full" .)
  if [ "$os" = "darwin" ]; then
    # A library of macOS carries the name a program linked to it looks
    # for, and Go writes the bare file name: each is named as found
    # through the program's own list of folders, @rpath, and libfadodb
    # finds DuckDB's library in its own folder, whatever name DuckDB
    # built it with. A changed library is signed again, as macOS asks.
    install_name_tool -id "@rpath/$core" "$out/$core"
    install_name_tool -id "@rpath/$full" "$out/$full"
    linked="$(otool -L "$out/$full" | awk '/libduckdb/ {print $1; exit}')"
    if [ -n "$linked" ] && [ "$linked" != "@rpath/$duck" ]; then
      install_name_tool -change "$linked" "@loader_path/$duck" "$out/$full"
    fi
    codesign -f -s - "$out/$core" "$out/$full"
  fi
  # Go writes a header of its own beside each library; fadodb.h is the
  # one for people, and a test holds the two to the same functions.
  rm -f "${out:?}/${core%.*}.h" "${out:?}/${full%.*}.h"
  cp cabi/fadodb.h "$out/fadodb.h"
  ls -l "$out/$core" "$out/$full" "$out/$duck"
  ;;
try)
  for library in "$out/$core" "$out/$full"; do
    [ -f "$library" ] || { echo "library: $library is not built; run scripts/library.sh build"; exit 1; }
    checked "$library"
  done
  tried "$out/$core"
  tried "$out/$full" must
  # The tests of the module that only the library with DuckDB has, built
  # with it and run with DuckDB's library where the system looks.
  duckdb_dir="$(native "$(pwd)/$out/duckdb")"
  case "$os" in
  linux) export LD_LIBRARY_PATH="$duckdb_dir${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}" ;;
  darwin) export DYLD_LIBRARY_PATH="$duckdb_dir" ;;
  windows) PATH="$(pwd)/$out/duckdb:$PATH"; export PATH ;;
  esac
  (cd cabi && CGO_ENABLED=1 CGO_LDFLAGS="-L$duckdb_dir" go test -count=1 -tags duckdb,duckdb_use_lib -run 'Engine' .)
  ;;
pack)
  for library in "$out/$core" "$out/$full"; do
    [ -f "$library" ] || { echo "library: $library is not built; run scripts/library.sh build"; exit 1; }
  done
  packed "fadodb-core-library_${version}_${os}_${arch}" "$out/$core"
  packed "fadodb-library_${version}_${os}_${arch}" "$out/$full" "$out/$duck" "$out/LICENSE-duckdb"
  [ -n "$python" ] || { echo "library: no Python to make the wheel with"; exit 1; }
  wheeled
  ;;
*)
  sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
