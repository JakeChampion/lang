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
# Go, wasmtime, wasm-tools and the WASI adapter are installed by mise from the
# repo's own mise.toml + mise.lock (COPYed in; scripts/devbox builds with the
# repo root as context and .dockerignore admits only what this file COPYs). No
# version is written here: a second copy of a pin is exactly what buys an
# opaque `invalid leading byte (0x43)` when it falls behind.
FROM debian:bookworm

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
      make git \
      xz-utils curl ca-certificates gdb \
 && rm -rf /var/lib/apt/lists/* \
 && for t in qemu-x86_64 qemu-aarch64; do \
      [ -e "/usr/bin/$t" ] || ln -sf "/usr/bin/$t-static" "/usr/bin/$t"; \
    done \
 && [ -e /usr/bin/aarch64-linux-gnu-gcc ] || ln -sf /usr/bin/gcc /usr/bin/aarch64-linux-gnu-gcc

# GNU coreutils' own `uptime`, which Debian does not ship: their coreutils
# package does not build it, and `/usr/bin/uptime` on the distributions that
# have one at all belongs to procps -- a different program with different
# options and different output. The corpus gate for uptime(1) has no oracle
# without this, and a gate with no oracle is a SKIP reporting `ok`.
#
# The version is PINNED rather than taken from the image, and it is not the
# image's own 9.1. Where uptime gets its boot time changed in 9.4: 9.1 reads
# /proc/uptime and lets it OVERRIDE the utmp BOOT_TIME record, so a corpus
# handing it a database would be measuring the container's real uptime
# instead of the fixture's. 9.4 and the 9.10 this project is held to both
# take the record, and 9.4 is what the CI image's own coreutils is.
#
# `make` in full rather than `make src/uptime`: a named target skips the
# gnulib header generation the build depends on and dies on a missing
# stdckdint.h.
ARG GNU_UPTIME_VERSION=9.4
RUN set -eux; \
    ver="$GNU_UPTIME_VERSION"; \
    cd /tmp; \
    curl -fsSLO "https://ftp.gnu.org/gnu/coreutils/coreutils-$ver.tar.xz"; \
    tar xf "coreutils-$ver.tar.xz"; \
    cd "coreutils-$ver"; \
    FORCE_UNSAFE_CONFIGURE=1 ./configure --quiet --disable-nls --without-selinux; \
    make -j"$(nproc)"; \
    install -D src/uptime /opt/gnu-coreutils/bin/uptime; \
    cd /; rm -rf "/tmp/coreutils-$ver" "/tmp/coreutils-$ver.tar.xz"; \
    /opt/gnu-coreutils/bin/uptime --version | head -1 | grep -q '(GNU coreutils)'

# hyperfine and python3 for scripts/coreutils-bench: it is the repo's own
# benchmark runner and could not run in the repo's own Linux image, which
# matters for the utilities that cannot run on the macOS host at all.
# Placed after the coreutils build so an edit here does not rebuild it.
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      hyperfine python3 \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /work
COPY mise.toml mise.lock /work/
COPY scripts/toolchain-env /work/scripts/toolchain-env

# The repo is mounted over /work at run time, so the shims resolve tools from
# the same mise.toml the image installed from; MISE_TRUSTED_CONFIG_PATHS keeps
# mise from refusing the mounted copy when it differs from the built-in one.
ENV MISE_TRUSTED_CONFIG_PATHS=/work MISE_YES=1
RUN set -eux; \
    HOME=/root scripts/toolchain-env >/dev/null; \
    ln -s "$(/root/.local/bin/mise where http:wasi-adapter)/wasi_snapshot_preview1.command.wasm" /opt/adapter.wasm

# GOPATH stays at the golang image's /go so the module-cache volume
# scripts/devbox mounts keeps its name.
ENV PATH=/root/.local/bin:/root/.local/share/mise/shims:$PATH \
    FERN_GNU_COREUTILS=/usr/bin:/opt/gnu-coreutils/bin \
    FERN_WASI_ADAPTER=/opt/adapter.wasm \
    GOPATH=/go \
    GOFLAGS=-buildvcs=false

# Fail the CONTAINER, not the tests, when a tool is missing. Every wasm and
# cross-arch e2e test skips on a failed lookup and a skipped test reports `ok`,
# so a half-built image would report a green sweep having run nothing.
RUN set -eux; \
    for t in go wasmtime wasm-tools qemu-x86_64 qemu-aarch64 x86_64-linux-gnu-gcc aarch64-linux-gnu-gcc hyperfine python3; do \
      command -v "$t" >/dev/null || (echo "missing tool in image: $t" >&2; exit 1); \
    done; \
    go version; wasmtime --version; wasm-tools --version; \
    test -f "$FERN_WASI_ADAPTER"; \
    /opt/gnu-coreutils/bin/uptime --version | head -1 | grep -q '(GNU coreutils)' \
      || (echo "the oracle uptime is not GNU coreutils; the corpus gate for it would have none" >&2; exit 1)
