# The image of FadoDB for containers: the command-line tool and its
# server, and nothing else. docs/manual/in-a-container.md is its
# chapter, and scripts/image.sh builds it, plays the chapter against it
# and publishes it with a release.
#
#   docker build -t fadodb .
#
# GO_VERSION is the Go of go.mod; a test holds the two together
# (internal/cli/container_test.go).
ARG GO_VERSION=1.26.11

# The tool in the image is fadodb-core, the program in pure Go, so it is
# built on the machine that builds the image, for the machine the image
# is for: nothing is emulated. It is started as fadodb, as in every
# image before; an image with the analytical engine is not built yet
# (docs/manual/in-a-container.md).
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=
ARG DATE=
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd cmd
COPY fado fado
COPY internal internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
      -o /image/fadodb ./cmd/fadodb-core
# What the tool needs around it:
#   /etc/ssl/certs  the authorities an action's request to another
#                   service is checked against
#   /data           where a database is kept, the user's own and closed
#                   to any other
#   /tmp            for a check of a class in a file and for a large
#                   sort
RUN mkdir -p /image/etc/ssl/certs /image/data /image/tmp \
 && cp /etc/ssl/certs/ca-certificates.crt /image/etc/ssl/certs/ \
 && chown 65532:65532 /image/data && chmod 0700 /image/data \
 && chmod 1777 /image/tmp

FROM scratch
ARG VERSION=dev
ARG COMMIT=
LABEL org.opencontainers.image.title="FadoDB" \
      org.opencontainers.image.description="An embeddable active document database: the command-line tool and its server" \
      org.opencontainers.image.source="https://github.com/MojoHand70/fadodb" \
      org.opencontainers.image.documentation="https://github.com/MojoHand70/fadodb/blob/main/docs/manual/in-a-container.md" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$COMMIT"
COPY --from=build /image /
# No user of the image is root. The number has no name: the image holds
# no list of users.
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
