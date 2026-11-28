# Dependency budget

The dependency gate (`scripts/depgate.sh`) fails the build when `go.mod` has a
direct requirement that is not listed in the table below. Adding a row needs a
justification and a reviewer who agrees with it. See
[ADR-0002](adr/0002-pure-go-core-and-dependency-budget.md).

## Core module (`github.com/MojoHand70/fadodb`)

| Module | License | Status | Justification |
|---|---|---|---|
| Go standard library | BSD-3 | in use | Everything the core needs for HTTP, JSON, crypto and testing. |
| `modernc.org/sqlite` | BSD-3 | in use | Pure-Go SQLite. Keeps the core free of cgo so it cross-compiles and embeds anywhere. Used by `internal/formula/sqlitefn` for the SQL conformance runner since Phase 1 (ADR-0020); the transactional store follows in Phase 2. |

### Files in the program

The server's own pages load files the program holds
(`internal/server/web`, ADR-0088, item 7). No code comes with them.

| File | Where it comes from | License | Why |
|---|---|---|---|
| Schibsted Grotesk, Latin and Latin Extended, as WOFF2 | Google Fonts, as Rhea serves it | SIL Open Font License 1.1, its text in `internal/server/web/fonts` | The typeface of words on the pages where people sign in, as on Rhea's sign-in (ADR-0088, the owner's ruling 3). |
| Martian Mono, Latin and Latin Extended, as WOFF2 | Google Fonts, as Rhea serves it | SIL Open Font License 1.1, its text in `internal/server/web/fonts` | The typeface of the server's address and of codes on those pages. |
| FadoDB's mark, `mark.png` | fadodb.org | FadoDB's own | The signet above each page's heading, and its icon. |

## The notices of the code of others

[THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) holds the notice of
every piece of others' code a program, an image or a library of FadoDB
carries: Go, its standard library and runtime; DuckDB; and every module
each is built from, for every system a release is built for. It travels
in every archive, in both images at `/licenses/`, and in every wheel
with a library. It is written from the modules, and a test holds it to
them (`internal/notices`; on GitHub with `FADODB_NOTICES=must`): a
module added, moved or dropped, by hand or by Dependabot, fails the
checks until `FADODB_UPDATE_NOTICES=1 go test ./internal/notices/`
writes it again.

## Separate modules

These live in their own Go modules so their weight and cgo never reach the core.

| Module | Used by | License | Why separate |
|---|---|---|---|
| `github.com/duckdb/duckdb-go/v2` with `go.duckdb.org/libduckdb/<os>-<arch>` | `analytics/` | MIT | Needs cgo and a C compiler; a minimal binary linking DuckDB 2.0.0 is 75 MB. Prebuilt libraries for five platforms; `duckdb_use_lib` links a system library instead. In use since Phase 1 for the DuckDB conformance runner (ADR-0020) and since Phase 2 for the DuckDB target of the store benchmarks (ADR-0017). Since Phase 8 it is the analytical engine of the program `fadodb` (`analytics/`, `analytics/cmd/fadodb`; ADR-0089), which keeps its copy of the documents in it. The library carries core_functions, icu, json and parquet, linked in; every connection to the copy turns off DuckDB's fetching of an extension from the network and its loading of one from the machine, so nothing else is fetched or loaded. |
| `github.com/jackc/pgx/v5` | `postgres/` | MIT | The driver for Postgres, in pure Go, and its tree are needed only by the Postgres store (ADR-0104). In use since Phase 10 for the conformance runner on Postgres; the store follows. No program links it yet, so `THIRD_PARTY_NOTICES.md` does not name it. Dependabot watches it: it speaks to a server over a network. |

The libraries of C, `cabi/` (ADR-0101, ADR-0102), are a module of their
own because they need cgo. `libfadodb-core` adds no dependency to those
of the core. `libfadodb`, built with the tag `duckdb`, adds the module
`analytics/` above, and is linked to DuckDB's own shared library, which
travels beside it: `scripts/library.sh` takes it from DuckDB's release
for the version the Go driver is built from (`duckdb-release.json` in
`github.com/duckdb/duckdb-go-bindings`), refuses a file whose checksum
is not the one the driver records, and puts DuckDB's notice (MIT)
beside it as `LICENSE-duckdb`. Like `analytics/go.mod`, `cabi/go.mod`
is tidied when a version in the core's or the analytics module's moves.

## The images for containers

There are two images (the `Dockerfile` at the root): that of `fadodb`,
with the analytical engine, and that of `fadodb-core`. Nothing here is
a Go module, so the dependency gate does not see it.

| What | Where it comes from | License | Why |
|---|---|---|---|
| `golang:<the Go of go.mod>-bookworm`, Docker's official image of Go on Debian 12 | Docker Hub | BSD-3 (Go) | Builds both programs. Nothing of it reaches the image of `fadodb`; of it, the image of `fadodb-core` has the list of certificate authorities and no other file. A test holds its version to the `go` line of `go.mod`. |
| Debian 12's compiler for the other kind of machine, `gcc` and `g++` for `aarch64` or `x86-64` | Debian's packages, fetched while the image is built | GPL-3.0 with the GCC runtime exception | DuckDB is C++, so `fadodb` for ARM is built on an Intel machine by a compiler for ARM, and the other way round. Nothing of the compiler reaches an image. |
| `gcr.io/distroless/cc-debian12:nonroot` | Google's "distroless" images, built from Debian 12 | each package under its own: glibc LGPL-2.1, libstdc++ and libgcc GPL-3.0 with the GCC runtime exception, OpenSSL Apache-2.0, the time zones public domain, the list of certificate authorities MPL-2.0 | The base of the image of `fadodb`: the C and C++ libraries DuckDB needs, the certificates, and a user who is not root. It has no shell and no program. |
| DuckDB, linked into `fadodb` | the module `analytics/` above | MIT | The analytical engine. Its notice is in the image at `/licenses/LICENSE-duckdb`, beside FadoDB's at `/licenses/LICENSE`, and in each archive of `fadodb` as `LICENSE-duckdb`. |
| The list of certificate authorities, `/etc/ssl/certs/ca-certificates.crt` | in the image of `fadodb`, the base's; in the image of `fadodb-core`, the `ca-certificates` package of the image the tool is built in. Both are Mozilla's list | MPL-2.0 | An action's request to another service is checked against it. The image of `fadodb-core` holds no other file but the tool and its licences. |

## The browser VM's build tools

The browser VM (ADR-0029) is written in TypeScript under `web/vm/` and
built into one plain JavaScript file. What builds it is listed in
`web/package.json`, pinned to an exact version, with its lock file
beside it. Nothing of it reaches the file a page loads: the file has no
dependency, so an audit of it is an audit of the one file. The Go
gates do not see these; the step `make vmtest` of the job `checks` uses
them.

| Tool | Version | License | Why |
|---|---|---|---|
| `typescript` | 7.0.2 | Apache-2.0 | Checks the types of the sources before every build. The VM's public surface is a contract (ADR-0029), and the checker holds it. |
| `esbuild` | 0.28.2 | MIT | Bundles the sources into one ES module and one classic script and minifies both, so that the size budget of 100 KB is measured on what a page loads. Its install script is not run; it finds its binary through its platform package. |

Node 24 runs the conformance suite on the built file, as CI does with
`actions/setup-node`.

## Developer tools

Installed with `go install`, never added to `go.mod`.

| Tool | Purpose |
|---|---|
| `honnef.co/go/tools/cmd/staticcheck` | Lint |
| `golang.org/x/vuln/cmd/govulncheck` | Vulnerability scan |
| `golang.org/x/perf/cmd/benchstat` | Benchmark comparison in the benchmark gate |
| `github.com/goreleaser/goreleaser/v2` | Release builds, run by the release workflow |
