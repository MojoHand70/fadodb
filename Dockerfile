# The images of FadoDB for containers: the command-line tool and its
# server, and what the tool needs to run. docs/manual/in-a-container.md
# is their chapter, and scripts/image.sh builds them, plays the chapter
# against the first and publishes them with a release.
#
#   docker build -t fadodb .                      fadodb, with the analytical engine
#   docker build -t fadodb-core --target core .   fadodb-core, in pure Go
#
# The two programs are one code (ADR-0089). The image built when no
# target is named holds fadodb, as the plain name of a program does.
#
# GO_VERSION is the Go of go.mod; a test holds the two together
# (internal/cli/container_test.go).
ARG GO_VERSION=1.26.11

# What both programs are built from, on the machine that builds the
# image: nothing is emulated.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS source
WORKDIR /src
COPY go.mod go.sum ./
COPY analytics analytics
COPY bench bench
COPY cmd cmd
COPY fado fado
COPY internal internal
COPY LICENSE THIRD_PARTY_NOTICES.md ./

# fadodb-core is pure Go, so it is built for the machine the image is
# for by the compiler of the machine that builds it.
FROM source AS core-build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=
ARG DATE=
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
      -o /image/fadodb-core ./cmd/fadodb-core
# What the tool needs around it:
#   /etc/ssl/certs  the authorities an action's request to another
#                   service is checked against
#   /data           where a database is kept, the user's own and closed
#                   to any other
#   /tmp            for a check of a class in a file and for a large
#                   sort
#   /licenses       FadoDB's licence and the notices of the code of others
#                   the tool carries
RUN mkdir -p /image/etc/ssl/certs /image/data /image/tmp /image/licenses \
 && cp /etc/ssl/certs/ca-certificates.crt /image/etc/ssl/certs/ \
 && cp LICENSE THIRD_PARTY_NOTICES.md /image/licenses/ \
 && chown 65532:65532 /image/data && chmod 0700 /image/data \
 && chmod 1777 /image/tmp

# fadodb has DuckDB in it, which is C++: it needs a C compiler for the
# machine the image is for. Where that is another machine than the one
# that builds, Debian's compiler for it is fetched; nothing is emulated
# here either.
FROM source AS build
ARG TARGETOS
ARG TARGETARCH
ARG BUILDARCH
ARG VERSION=dev
ARG COMMIT=
ARG DATE=
RUN if [ "$TARGETARCH" != "$BUILDARCH" ]; then \
      case "$TARGETARCH" in \
        arm64) packages="gcc-aarch64-linux-gnu g++-aarch64-linux-gnu" ;; \
        amd64) packages="gcc-x86-64-linux-gnu g++-x86-64-linux-gnu" ;; \
        *) echo "fadodb is not built for $TARGETARCH: DuckDB has no library for it" >&2; exit 1 ;; \
      esac; \
      apt-get update && apt-get install -y --no-install-recommends $packages && rm -rf /var/lib/apt/lists/*; \
    fi
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    if [ "$TARGETARCH" != "$BUILDARCH" ]; then \
      case "$TARGETARCH" in \
        arm64) export CC=aarch64-linux-gnu-gcc CXX=aarch64-linux-gnu-g++ ;; \
        amd64) export CC=x86_64-linux-gnu-gcc CXX=x86_64-linux-gnu-g++ ;; \
      esac; \
    fi; \
    cd analytics && CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
      -o /image/fadodb ./cmd/fadodb && \
    mkdir -p /image/licenses && \
    cp "$(go list -m -f '{{.Dir}}' github.com/duckdb/duckdb-go-bindings)/LICENSE" /image/licenses/LICENSE-duckdb && \
    cp /src/LICENSE /src/THIRD_PARTY_NOTICES.md /image/licenses/
# /data is where a database and its copy are kept, the user's own and
# closed to any other. The certificates, /tmp and the C and C++
# libraries the program needs are the base image's.
RUN mkdir -p /image/data && chown 65532:65532 /image/data && chmod 0700 /image/data

# The image of fadodb-core: the program, the certificates, the licences
# and nothing else.
FROM scratch AS core
ARG VERSION=dev
ARG COMMIT=
LABEL org.opencontainers.image.title="FadoDB core" \
      org.opencontainers.image.description="An embeddable active document database: the command-line tool and its server, in pure Go, without the analytical engine" \
      org.opencontainers.image.source="https://github.com/MojoHand70/fadodb" \
      org.opencontainers.image.documentation="https://github.com/MojoHand70/fadodb/blob/main/docs/manual/in-a-container.md" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$COMMIT"
COPY --from=core-build /image /
# No user of the image is root. The number has no name: the image holds
# no list of users.
USER 65532:65532
WORKDIR /data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/fadodb-core"]
CMD ["help"]

# The image of fadodb, with the analytical engine: the program, on a
# base that has the C and C++ libraries DuckDB needs, the certificates,
# the licences of FadoDB and of DuckDB, which is linked into the
# program, and no shell or other program.
FROM gcr.io/distroless/cc-debian12:nonroot
ARG VERSION=dev
ARG COMMIT=
LABEL org.opencontainers.image.title="FadoDB" \
      org.opencontainers.image.description="An embeddable active document database: the command-line tool and its server, with the analytical engine" \
      org.opencontainers.image.source="https://github.com/MojoHand70/fadodb" \
      org.opencontainers.image.documentation="https://github.com/MojoHand70/fadodb/blob/main/docs/manual/in-a-container.md" \
      org.opencontainers.image.licenses="Apache-2.0 AND MIT" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$COMMIT"
COPY --from=build /image /
# No user of the image is root: the user 65532, which the base calls
# nonroot.
USER 65532:65532
WORKDIR /data
VOLUME /data
EXPOSE 8080
# The image is the tool: what follows its name is what follows
# "fadodb" at a terminal. Told nothing, it prints its help. It serves
# nothing by itself, since a server that listens beyond its container
# has to be told so (--plain-http or a certificate).
ENTRYPOINT ["/fadodb"]
CMD ["help"]
