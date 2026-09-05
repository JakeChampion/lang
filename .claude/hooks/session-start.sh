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

# --- the pinned toolchain: Go, wasmtime, wasm-tools, the WASI adapter ---
# All from mise.toml + mise.lock, the one place a version lives, through the
# same bootstrap the Netlify build and scripts/devbox use. The wasm toolchain
# IS provisioned locally as well as remotely: wasm executes host-independently,
# so a Mac runs those legs as faithfully as a runner does, and without the
# pinned pair every wasm test SKIPs into a false `ok`.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
exports="$("$ROOT/scripts/toolchain-env")"

# The checked-in pre-push hook runs the cheap lint gates before a push
# (docs/LOCAL-DEV-LOOP.md); core.hooksPath is per-clone config.
git -C "$ROOT" config core.hooksPath .githooks

# Persist for the session. CLAUDE_ENV_FILE is not always set outside the
# managed container; the install above is still worth doing there, so this is
# a guard rather than a hard need.
if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
  printf '%s\n' "$exports" >> "$CLAUDE_ENV_FILE"
else
  echo "session-start: toolchain installed by mise; eval \"\$(scripts/toolchain-env)\" to use it" >&2
fi
