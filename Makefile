# FadoDB build and quality gates. Every target here also runs in CI.

# The two programs (ADR-0089): fadodb-core in pure Go, and fadodb, the
# same program with the analytical engine.
CORE    := bin/fadodb-core
FULL    := bin/fadodb
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Developer tools are installed with `go install`, never added to go.mod.
STATICCHECK_VERSION ?= latest
GOVULNCHECK_VERSION ?= latest
BENCHSTAT_VERSION   ?= latest

.PHONY: all build full library cabi run test analytics postgres bench fmt vet lint vuln tools depgate sizegate filegate benchgate gates clean docs image imagecheck vm vmtest

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(CORE) ./cmd/fadodb-core

# The same program with the analytical engine, DuckDB, linked in
# (ADR-0017, ADR-0089): fadodb. It is built in the DuckDB module, with
# cgo and a C compiler, and is no part of the gates of the core.
full:
	cd analytics && CGO_ENABLED=1 go build -trimpath -ldflags '$(LDFLAGS)' -o ../$(FULL) ./cmd/fadodb

# The library of C (ADR-0101): the engine of fadodb-core as a shared
# library, with fadodb.h beside it, for a program of any language that
# opens a database in its own process. It is built in the module cabi,
# with cgo and a C compiler, and is no part of the gates of the core.
ifeq ($(shell go env GOOS),windows)
LIBRARY := bin/fadodb-core.dll
else ifeq ($(shell go env GOOS),darwin)
LIBRARY := bin/libfadodb-core.dylib
else
LIBRARY := bin/libfadodb-core.so
endif

library:
	cd cabi && CGO_ENABLED=1 go build -buildmode=c-shared -trimpath -ldflags '$(LDFLAGS)' -o ../$(LIBRARY) .
	rm -f $(basename $(LIBRARY)).h
	cp cabi/fadodb.h bin/fadodb.h

run: build
	$(CORE) version

test:
	go test -race -count=1 ./...

# The DuckDB module is a separate Go module and needs cgo and a C compiler.
analytics:
	cd analytics && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test -count=1 ./...

# The module of the library: vetted, and tested, which builds the
# library and tries it from a program of C; it needs a C compiler.
cabi:
	cd cabi && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 FADODB_CABI=must go test -count=1 ./...

# The Postgres module (ADR-0104) is a separate Go module, with the pgx
# driver. Its tests need a Postgres 17 or later, named in FADODB_POSTGRES
# as postgres://user:password@host:port/database: each run makes a schema
# of its own there and drops it. With no address they are skipped;
# FADODB_POSTGRES=must fails them, as on GitHub, where a skipped test
# would not show.
postgres:
	cd postgres && go vet ./... && go test -count=1 ./...

# docs writes the generated chapters of the manual again: the list of
# functions and the list of refusal codes, from the engine's own tables.
docs:
	FADODB_UPDATE_DOCS=1 go test -count=1 -run TestGenerated ./internal/docgen/

bench:
	go test -run '^$$' -bench . -benchmem -count 6 ./...

fmt:
	@files=$$(gofmt -l $$(git ls-files -co --exclude-standard '*.go')); \
	if [ -n "$$files" ]; then echo "gofmt needed:"; echo "$$files"; exit 1; fi

vet:
	go vet ./...

lint: fmt vet
	staticcheck ./...

vuln:
	govulncheck ./...

tools:
	go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install golang.org/x/perf/cmd/benchstat@$(BENCHSTAT_VERSION)

depgate:
	scripts/depgate.sh

sizegate:
	scripts/sizegate.sh

# No tracked file is larger than 1 MB: a build output is never committed.
filegate:
	scripts/filegate.sh

# BASE is the git ref to compare benchmarks against.
benchgate:
	scripts/benchgate.sh $(BASE)

gates: lint vuln depgate sizegate filegate test

# The image for containers (the Dockerfile): image builds it for this
# machine as "fadodb"; imagecheck builds it for every machine a release
# publishes it for and plays its chapter of the manual against it, as
# GitHub does on every push. Both need Docker.
image:
	scripts/image.sh build

imagecheck:
	scripts/image.sh check

clean:
	rm -rf bin dist

# The browser VM (ADR-0029): built into web/vm/dist with the build tools
# of web/package.json; its declarations checked as a page would use them
# and against the module; then given every case of the conformance
# suite, compiled by the engine, in Node; and the recalculation budget of
# ADR-0003 measured on the built file. Needs Node 24 and npm.
vm:
	cd web && npm ci --no-audit --no-fund && npm run build

vmtest: vm
	cd web && npm run check
	go run ./web/vm/cases > web/vm/dist/cases.json
	node web/vm/test/run.mjs web/vm/dist/cases.json
	node web/vm/test/bench.mjs web/vm/dist/cases.json
	go run ./web/vm/bundle > web/vm/dist/bundle.json
	node web/vm/test/bundle.mjs web/vm/dist/bundle.json
