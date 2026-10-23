#!/usr/bin/env bash
# Compiles the server, builds the container image, and saves the image where
# Netlify finds functions.
#
# With no container tool set, this installs a static, rootless Podman set up
# for an unprivileged build sandbox. Set CONTAINER_TOOL=docker (or podman) to
# use a tool that is already installed.
# Set FERN to use an installed compiler in place of the nightly release.
set -euo pipefail
cd "$(dirname "$0")"

IMAGE=fern-server
OUTPUT=netlify/functions/fern-server.tar
FERN_RELEASE="${FERN_RELEASE:-https://github.com/JakeChampion/lang/releases/download/nightly}"
mkdir -p "$(dirname "$OUTPUT")" public
rm -f "$OUTPUT"

sha256_check() {
  if command -v sha256sum > /dev/null; then sha256sum -c "$@"; else shasum -a 256 -c "$@"; fi
}

if [ -z "${FERN:-}" ]; then
  case "$(uname -s)-$(uname -m)" in
    Linux-x86_64) release=fern-linux-x86_64 ;;
    Linux-aarch64) release=fern-linux-arm64 ;;
    Darwin-arm64) release=fern-darwin-arm64 ;;
    *) echo "build.sh: no fern release for $(uname -s) $(uname -m); set FERN" >&2; exit 1 ;;
  esac
  dir="$(mktemp -d)"
  (cd "$dir" && curl -sSfLO "$FERN_RELEASE/$release.tar.gz" && curl -sSfLO "$FERN_RELEASE/$release.tar.gz.sha256" &&
    sha256_check "$release.tar.gz.sha256" && tar xzf "$release.tar.gz")
  FERN="$dir/fern"
fi
"$FERN" -target x86-64-linux -O -o server server.fern

if [ -n "${CONTAINER_TOOL:-}" ]; then
  "$CONTAINER_TOOL" build --platform linux/amd64 -t "$IMAGE" .
  "$CONTAINER_TOOL" save -o "$OUTPUT" "$IMAGE"
  ls -lh "$OUTPUT"
  exit 0
fi

# 4.x is the last line that runs on cgroups v1, which the build sandbox has.
PODMAN_VERSION=4.9.5
case "$(uname -m)" in
  x86_64) arch=amd64 sha=05fcac12a6777c4db5f90ecf71b859d669c7bb1a8ee39d9ab299ac9ad1f99c5d ;;
  aarch64) arch=arm64 sha=4df919133b7f0ed185aef823a5bc831a4680286e9758dde5f754109277fa605b ;;
  *) echo "build.sh: no static podman for $(uname -m); set CONTAINER_TOOL" >&2; exit 1 ;;
esac
PREFIX="${PODMAN_PREFIX:-$HOME/.cache/podman-$PODMAN_VERSION}"
PODMAN="$PREFIX/usr/local/bin/podman"

if [ ! -x "$PODMAN" ]; then
  echo "Installing podman $PODMAN_VERSION"
  tarball="$(mktemp -d)/podman.tar.gz"
  curl -sSfL "https://github.com/mgoltzsche/podman-static/releases/download/v$PODMAN_VERSION/podman-linux-$arch.tar.gz" -o "$tarball"
  echo "$sha  $tarball" | sha256sum -c -
  mkdir -p "$PREFIX"
  tar xzf "$tarball" -C "$PREFIX" --strip-components=1
fi

# The archive expects to unpack at /, and the build machine has no
# /etc/containers, so every path podman needs is set here.
export XDG_RUNTIME_DIR="$PREFIX/xdg"
mkdir -p "$XDG_RUNTIME_DIR" "$PREFIX/libpod" "$PREFIX/volumes" "$PREFIX/tmp"
chmod 700 "$XDG_RUNTIME_DIR"

export CONTAINERS_CONF="$PREFIX/containers.conf"
cat > "$CONTAINERS_CONF" <<CONF
[engine]
cgroup_manager = "cgroupfs"
events_logger = "file"
runtime = "runc"
conmon_path = ["$PREFIX/usr/local/lib/podman/conmon"]
helper_binaries_dir = ["$PREFIX/usr/local/lib/podman", "$PREFIX/usr/local/bin"]
static_dir = "$PREFIX/libpod"
volume_path = "$PREFIX/volumes"
tmp_dir = "$PREFIX/tmp"

[engine.runtimes]
runc = ["$PREFIX/usr/local/bin/runc"]
CONF

export CONTAINERS_REGISTRIES_CONF="$PREFIX/registries.conf"
echo 'unqualified-search-registries = ["docker.io"]' > "$CONTAINERS_REGISTRIES_CONF"

# vfs because the sandbox allows neither unprivileged overlay mounts nor
# fuse-overlayfs. ignore_chown_errors lets it work with one UID mapped.
export CONTAINERS_STORAGE_CONF="$PREFIX/storage.conf"
cat > "$CONTAINERS_STORAGE_CONF" <<CONF
[storage]
driver = "vfs"
graphroot = "$PREFIX/storage"
runroot = "$PREFIX/run"
rootless_storage_path = "$PREFIX/storage"

[storage.options]
ignore_chown_errors = "true"
CONF

# Rootless podman reads the signature policy only from this path.
POLICY="$HOME/.config/containers/policy.json"
if [ ! -f "$POLICY" ]; then
  mkdir -p "$(dirname "$POLICY")"
  echo '{"default": [{"type": "insecureAcceptAnything"}]}' > "$POLICY"
fi

# Without setuid newuidmap, podman can map only the build user's own UID. A
# $USER with no /etc/subuid entry makes it use that single mapping.
PODMAN_USER="${USER:-}"
if ! command -v newuidmap > /dev/null; then
  PODMAN_USER=podman-single-uid-mapping
fi

USER="$PODMAN_USER" "$PODMAN" build --platform linux/amd64 -t "$IMAGE" .
USER="$PODMAN_USER" "$PODMAN" save --format docker-archive -o "$OUTPUT" "$IMAGE"
ls -lh "$OUTPUT"
