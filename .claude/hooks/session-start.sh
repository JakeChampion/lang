#!/bin/bash
# SessionStart hook — provision the full Fern differential-test toolchain
# in Claude Code on the web so every session can run the e2e suite across
# ALL backends (x86_64 native, wasm via wasmtime, arm64 via qemu) and the
# RC free-on/free-off differential gate locally.
#
# Why this matters: the RC + Perceus reclamation work (docs/RC-PERCEUS-PLAN.md)
# adds free/reuse on every backend, and the arm64 differential gate is the
# non-negotiable check — qemu user-mode is where over-release shows up that
# native masks. Without qemu + the wasm tools a session SKIPs exactly the
# tests that guard the most dangerous code, so we install them up front.
set -euo pipefail

# --- arm64 cross-toolchain + qemu (arm64 e2e + RC differential gate) ---
# Linux only, and only in the ephemeral remote container: qemu's user-mode
# emulation does not exist on macOS (Homebrew's qemu is system emulation), and
# a local machine's packages are not ours to install. On a Mac these legs come
# from scripts/devbox instead.
if [ "${CLAUDE_CODE_REMOTE:-}" = "true" ] && [ "$(uname -s)" = "Linux" ] \
   && { ! command -v qemu-aarch64 >/dev/null 2>&1 || ! command -v aarch64-linux-gnu-gcc >/dev/null 2>&1; }; then
  # The base image carries a couple of third-party PPAs (deadsnakes,
  # ondrej/php) that 403 and fail `apt-get update`; the packages we need
  # are in the main Ubuntu archive, so tolerate the PPA errors.
  apt-get update 2>/dev/null || true
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    qemu-user-static gcc-aarch64-linux-gnu >/dev/null
  # The CLI + e2e harness look for `qemu-aarch64`; the package ships it as
  # `qemu-aarch64-static`. Symlink so both names resolve.
  if ! command -v qemu-aarch64 >/dev/null 2>&1; then
    ln -sf "$(command -v qemu-aarch64-static)" /usr/local/bin/qemu-aarch64
  fi
fi

# --- wasmtime + wasm-tools + WASI preview1 adapter (wasm e2e suite) ---
# Versions READ from .github/actions/setup-fern, so they cannot fall behind it.
# They used to be copied here, and the comment beside the copy still described
# the v37 / 1.240 pins long after CI had moved to v46 / 1.253 — a pin that
# drifts buys an opaque `invalid leading byte (0x43)` from the Preview-3 async
# tests rather than anything naming a version. See scripts/wasm-toolchain-pins.
WT_DIR="$HOME/.fern-wasm"
eval "$("$(dirname "${BASH_SOURCE[0]}")/../../scripts/wasm-toolchain-pins")"
WASMTIME_VER="v$WASMTIME_VER"
# The wasm toolchain IS provisioned locally as well as remotely: wasm executes
# host-independently, so a Mac runs those legs as faithfully as a runner does,
# and without the pinned pair every wasm test SKIPs into a false `ok`.
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64)   WT_ARCH="x86_64-linux" ;;
  Linux/aarch64)  WT_ARCH="aarch64-linux" ;;
  Darwin/arm64)   WT_ARCH="aarch64-macos" ;;
  Darwin/x86_64)  WT_ARCH="x86_64-macos" ;;
  *) echo "unsupported host $(uname -s)/$(uname -m)" >&2; exit 0 ;;
esac
mkdir -p "$WT_DIR"
# Version-check the cached binaries, not just their existence — a container
# provisioned under an older pin must refresh (the adapter ships per-wasmtime
# release, so it is re-fetched whenever the wasmtime binary is).
if [ ! -x "$WT_DIR/wasmtime" ] || ! "$WT_DIR/wasmtime" --version 2>/dev/null | grep -qF "${WASMTIME_VER#v}"; then
  curl -sSfL "https://github.com/bytecodealliance/wasmtime/releases/download/${WASMTIME_VER}/wasmtime-${WASMTIME_VER}-${WT_ARCH}.tar.xz" \
    | tar -xJ -C "$WT_DIR" --strip-components=1 --wildcards '*/wasmtime'
  curl -sSfL -o "$WT_DIR/adapter.wasm" \
    "https://github.com/bytecodealliance/wasmtime/releases/download/${WASMTIME_VER}/wasi_snapshot_preview1.command.wasm"
fi
if [ ! -x "$WT_DIR/wasm-tools" ] || ! "$WT_DIR/wasm-tools" --version 2>/dev/null | grep -qF "${WASMTOOLS_VER}"; then
  curl -sSfL "https://github.com/bytecodealliance/wasm-tools/releases/download/v${WASMTOOLS_VER}/wasm-tools-${WASMTOOLS_VER}-${WT_ARCH}.tar.gz" \
    | tar -xz -C "$WT_DIR" --strip-components=1 --wildcards '*/wasm-tools'
fi

# Persist for the session: tools on PATH + the adapter the e2e tests read
# via FERN_WASI_ADAPTER (so the wasm e2e cases RUN instead of SKIP).
# CLAUDE_ENV_FILE is not always set outside the managed container; the install
# above is still worth doing there, so this is a guard rather than a hard need.
if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
  {
    echo "export PATH=\"$WT_DIR:\$PATH\""
    echo "export FERN_WASI_ADAPTER=\"$WT_DIR/adapter.wasm\""
  } >> "$CLAUDE_ENV_FILE"
else
  echo "session-start: wasm toolchain at $WT_DIR (add it to PATH and set FERN_WASI_ADAPTER=$WT_DIR/adapter.wasm)" >&2
fi
