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

# Local dev machines bring their own toolchains; only provision in the
# ephemeral remote container.
if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

# --- arm64 cross-toolchain + qemu (arm64 e2e + RC differential gate) ---
if ! command -v qemu-aarch64 >/dev/null 2>&1 || ! command -v aarch64-linux-gnu-gcc >/dev/null 2>&1; then
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
# Versions pinned to match .github/actions/setup-fern (bump together).
# wasmtime v37 is required for the WASI P3 async tests (component-model-async
# is compiled out of v34) and wasm-tools 1.240 for composing the extern-variant
# provider components — older pins fail ~40 wasm e2e tests that CI passes.
WT_DIR="$HOME/.fern-wasm"
WASMTIME_VER="v37.0.1"
WASMTOOLS_VER="1.240.0"
case "$(uname -m)" in
  x86_64)  WT_ARCH="x86_64-linux" ;;
  aarch64) WT_ARCH="aarch64-linux" ;;
  *) echo "unsupported host arch $(uname -m)" >&2; exit 1 ;;
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
{
  echo "export PATH=\"$WT_DIR:\$PATH\""
  echo "export FERN_WASI_ADAPTER=\"$WT_DIR/adapter.wasm\""
} >> "$CLAUDE_ENV_FILE"
