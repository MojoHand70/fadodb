# FadoDB build and quality gates. Every target here also runs in CI.

BIN     := bin/fadodb
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Developer tools are installed with `go install`, never added to go.mod.
STATICCHECK_VERSION ?= latest
GOVULNCHECK_VERSION ?= latest
BENCHSTAT_VERSION   ?= latest

.PHONY: all build run test analytics bench fmt vet lint vuln tools depgate sizegate benchgate gates clean

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/fadodb

run: build
	$(BIN) version

test:
	go test -race -count=1 ./...

# The DuckDB module is a separate Go module and needs cgo and a C compiler.
analytics:
	cd analytics && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test -count=1 ./...

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

# BASE is the git ref to compare benchmarks against.
benchgate:
	scripts/benchgate.sh $(BASE)

gates: lint vuln depgate sizegate test

clean:
	rm -rf bin dist
