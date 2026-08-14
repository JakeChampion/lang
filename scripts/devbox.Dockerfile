# Linux toolchain for the legs a macOS host cannot run natively.
#
# Both native Fern targets emit Linux ELF, and the e2e harness reaches for
# `qemu-x86_64` / `qemu-aarch64` / `aarch64-linux-gnu-gcc` by name. macOS has
# none of them and cannot: qemu's user-mode emulation is Linux-only, and
# Homebrew's qemu is system emulation. So on a Mac those legs SKIP, and a SKIP
# reports `ok`.
#
# Built for linux/arm64 so that on Apple Silicon the aarch64 leg runs NATIVELY
# and only the x86-64 leg pays emulation. Building linux/amd64 instead inverts
# that and emulates the leg you run most.
#
# The wasm pins arrive as build args from scripts/devbox, which reads them from
# scripts/wasm-toolchain-pins. Do not hardcode them here: a second copy of the
# pins is exactly what buys an opaque `invalid leading byte (0x43)` when it
# falls behind.
FROM golang:1.24-bookworm

ARG WASMTIME_VER
ARG WASMTOOLS_VER
ARG TARGETARCH

RUN test -n "$WASMTIME_VER" -a -n "$WASMTOOLS_VER" \
    || (echo "build args WASMTIME_VER / WASMTOOLS_VER are required" >&2; exit 1)

# qemu-user-static carries the user-mode emulators for BOTH arches, so one
# image runs either leg. The x86-64 cross-gcc links the x86-64 ELF that the
# native backend emits; on an arm64 base the aarch64 compiler is the system
# gcc, which the harness still expects to find under its cross name.
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      qemu-user-static \
      gcc-x86-64-linux-gnu \
      gcc \
      libc6-dev \
      xz-utils curl ca-certificates gdb \
 && rm -rf /var/lib/apt/lists/* \
 && for t in qemu-x86_64 qemu-aarch64; do \
      [ -e "/usr/bin/$t" ] || ln -sf "/usr/bin/$t-static" "/usr/bin/$t"; \
    done \
 && [ -e /usr/bin/aarch64-linux-gnu-gcc ] || ln -sf /usr/bin/gcc /usr/bin/aarch64-linux-gnu-gcc

# wasmtime + wasm-tools + the WASI preview1 adapter, same versions and the same
# FERN_WASI_ADAPTER wiring the CI action uses, so the wasm legs behave here as
# they do on a runner.
RUN set -eux; \
    case "$TARGETARCH" in \
      arm64) WT_ARCH=aarch64-linux ;; \
      amd64) WT_ARCH=x86_64-linux ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    mkdir -p /opt/wasm; \
    curl -sSfL "https://github.com/bytecodealliance/wasmtime/releases/download/v${WASMTIME_VER}/wasmtime-v${WASMTIME_VER}-${WT_ARCH}.tar.xz" \
      | tar -xJ -C /opt/wasm --strip-components=1 "wasmtime-v${WASMTIME_VER}-${WT_ARCH}/wasmtime"; \
    curl -sSfL "https://github.com/bytecodealliance/wasm-tools/releases/download/v${WASMTOOLS_VER}/wasm-tools-${WASMTOOLS_VER}-${WT_ARCH}.tar.gz" \
      | tar -xz -C /opt/wasm --strip-components=1 "wasm-tools-${WASMTOOLS_VER}-${WT_ARCH}/wasm-tools"; \
    curl -sSfL -o /opt/wasm/adapter.wasm \
      "https://github.com/bytecodealliance/wasmtime/releases/download/v${WASMTIME_VER}/wasi_snapshot_preview1.command.wasm"

ENV PATH=/opt/wasm:$PATH \
    FERN_WASI_ADAPTER=/opt/wasm/adapter.wasm \
    GOFLAGS=-buildvcs=false

WORKDIR /work

# Fail the CONTAINER, not the tests, when a tool is missing. Every wasm and
# cross-arch e2e test skips on a failed lookup and a skipped test reports `ok`,
# so a half-built image would report a green sweep having run nothing.
RUN set -eux; \
    for t in wasmtime wasm-tools qemu-x86_64 qemu-aarch64 x86_64-linux-gnu-gcc aarch64-linux-gnu-gcc; do \
      command -v "$t" >/dev/null || (echo "missing tool in image: $t" >&2; exit 1); \
    done; \
    test -f "$FERN_WASI_ADAPTER"
