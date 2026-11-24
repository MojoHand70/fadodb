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

## Separate modules

These live in their own Go modules so their weight and cgo never reach the core.

| Module | Used by | License | Why separate |
|---|---|---|---|
| `github.com/duckdb/duckdb-go/v2` with `go.duckdb.org/libduckdb/<os>-<arch>` | `analytics/` | MIT | Needs cgo and a C compiler; a minimal binary linking DuckDB 2.0.0 is 75 MB. Prebuilt libraries for five platforms; `duckdb_use_lib` links a system library instead. In use since Phase 1 for the DuckDB conformance runner (ADR-0020) and since Phase 2 for the DuckDB target of the store benchmarks (ADR-0017). |
| `github.com/jackc/pgx/v5` | `postgres/` | MIT | Driver and its tree are only needed by the enterprise backend. |

## The image for containers

The image (the `Dockerfile` at the root) holds the tool and one file
that is not the project's. Nothing here is a Go module, so the
dependency gate does not see it.

| What | Where it comes from | License | Why |
|---|---|---|---|
| The list of certificate authorities, `/etc/ssl/certs/ca-certificates.crt` | the `ca-certificates` package of the image the tool is built in, which is Mozilla's list | MPL-2.0 | An action's request to another service is checked against it. The image holds no other file but the tool. |
| `golang:<the Go of go.mod>-alpine`, Docker's official image of Go | Docker Hub | BSD-3 (Go); nothing of Alpine is passed on | Builds the tool. Nothing of it reaches the image but the list above. A test holds its version to the `go` line of `go.mod`. |

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
