#!/usr/bin/env bash
# The image of FadoDB for containers (the Dockerfile at the root).
#
#   scripts/image.sh build [NAME]   build it for this machine, as NAME (fadodb)
#   scripts/image.sh check          build it for every machine a release
#                                   publishes it for, and play its chapter
#                                   of the manual against this machine's
#   scripts/image.sh push VERSION   check, then publish it under VERSION
#
# check is what GitHub runs on every push; push is what a release runs,
# signed in to the registry. An image is published only after its
# chapter, docs/manual/in-a-container.md, was played against it.
set -euo pipefail
cd "$(dirname "$0")/.."

# Where a release publishes the image, and the machines it is for.
REGISTRY="${FADODB_REGISTRY:-ghcr.io/mojohand70/fadodb}"
PLATFORMS="linux/amd64,linux/arm64"

commit="$(git rev-parse --short HEAD 2>/dev/null || echo "")"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# The image says its version as the tool does: "fadodb version", the
# health of a server and the labels of the image.
stamp() {
  printf -- '--build-arg\nVERSION=%s\n--build-arg\nCOMMIT=%s\n--build-arg\nDATE=%s\n' "$1" "$commit" "$date"
}

# An image for several machines is more than the builder of a plain
# Docker keeps, unless Docker keeps its images in containerd. Where it
# does not, the images are built by a builder of their own.
builder() {
  if docker info --format '{{.DriverStatus}}' 2>/dev/null | grep -q 'io.containerd.snapshotter'; then
    return
  fi
  if ! docker buildx inspect fadodb-image >/dev/null 2>&1; then
    docker buildx create --name fadodb-image --driver docker-container >/dev/null
  fi
  echo "--builder=fadodb-image"
}

check() {
  local version="$1" name="$2"
  mapfile -t args < <(stamp "$version")
  mapfile -t with < <(builder)
  echo "image: building for $PLATFORMS"
  docker buildx build ${with[@]+"${with[@]}"} --platform "$PLATFORMS" "${args[@]}" .
  echo "image: playing the chapter against this machine's"
  docker buildx build ${with[@]+"${with[@]}"} --load --tag "$name" "${args[@]}" .
  FADODB_IMAGE="$name" go test -count=1 -run 'Container' ./cmd/fadodb
}

case "${1:-}" in
  build)
    mapfile -t args < <(stamp "$(git describe --tags --always --dirty 2>/dev/null || echo dev)")
    docker build --tag "${2:-fadodb}" "${args[@]}" .
    ;;
  check)
    check dev fadodb:check
    echo "image: ok"
    ;;
  push)
    version="${2:?scripts/image.sh push VERSION, such as 0.6.0}"
    case "$version" in
      v*|*[!0-9A-Za-z.-]*) echo "image: a version is written as 0.6.0, and this is $version" >&2; exit 2 ;;
    esac
    check "$version" "fadodb:$version"
    mapfile -t args < <(stamp "$version")
    mapfile -t with < <(builder)
    echo "image: publishing $REGISTRY:$version for $PLATFORMS"
    docker buildx build ${with[@]+"${with[@]}"} --platform "$PLATFORMS" --tag "$REGISTRY:$version" --push "${args[@]}" .
    ;;
  *)
    sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
    ;;
esac
