#!/usr/bin/env bash
#
# bootstrap/bootstrap.sh — build the self-host compiler from a checkout with
# NO Go toolchain and NO native backend on the path.
#
#   bootstrap.sh build       (make bootstrap)  stage0 -> stage1, install it
#   bootstrap.sh distcheck   (make distcheck)  stage1 -> stage2, stage1 == stage2
#
#   stage0   a pinned earlier compiler (bootstrap/stage0.lock), or the binary
#            named by STAGE0=<path>
#   stage1   stage0 compiles examples/self_host/fern.fern for this host; it
#            must then compile and run a small program (the smoke test), and
#            is installed as bin/fern-selfhost — the artifact `make
#            selfhost-cli` builds with the native toolchain
#   stage2   stage1 compiles the same source. stage1 and stage2 must be
#            byte-identical: a compiler that reproduces itself through its own
#            output is the reproducibility gate, and a difference is either
#            nondeterminism or a miscompile that changed the compiler's own
#            behaviour
#
# The pinned stage0 is downloaded once into build/bootstrap/ and verified
# against the lock's sha256 of the UNCOMPRESSED binary before it runs.
# docs/BOOTSTRAP.md is the runbook: refreshing the pin, debugging a
# stage1 != stage2 divergence, what the pin does and does not prove.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCK="$ROOT/bootstrap/stage0.lock"
OUT="$ROOT/build/bootstrap"
ENTRY="$ROOT/examples/self_host/fern.fern"
STDLIB="$ROOT/internal/stdlib"

die() { echo "bootstrap: $*" >&2; exit 1; }

mode="${1:-build}"
case "$mode" in build|distcheck) ;; *) die "usage: bootstrap.sh [build|distcheck]" ;; esac

case "$(uname -s)/$(uname -m)" in
  Linux/x86_64)  HOST=x86-64-linux ;;
  Linux/aarch64) HOST=arm64-linux ;;
  Darwin/arm64)  HOST=arm64-darwin ;;
  *) die "unsupported bootstrap host $(uname -s)/$(uname -m) (x86-64 Linux, arm64 Linux, arm64 macOS)" ;;
esac

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

size() { wc -c < "$1" | tr -d ' '; }

# lock_field KEY prints the value of the lock's `KEY value` line.
lock_field() {
  local v
  v="$(awk -v k="$1" '$1 == k { print $2; exit }' "$LOCK")"
  [ -n "$v" ] || die "$LOCK has no '$1' line"
  echo "$v"
}

# resolve_stage0 sets $stage0 to an executable: STAGE0=<path> if given, else
# the lock's pin for this host, fetched into the cache on first use.
resolve_stage0() {
  if [ -n "${STAGE0:-}" ]; then
    [ -x "$STAGE0" ] || die "STAGE0=$STAGE0 is not an executable file"
    stage0="$(cd "$(dirname "$STAGE0")" && pwd)/$(basename "$STAGE0")"
    echo "stage0: $stage0 (local, sha256 $(sha256 "$stage0"))"
    return
  fi
  [ -f "$LOCK" ] || die "no $LOCK and no STAGE0=<path> given"
  local url want tag asset got
  url="$(lock_field url)"
  want="$(lock_field "$HOST")"
  tag="${url##*/}"
  stage0="$OUT/stage0/$tag/fern-selfhost-$HOST"
  if [ ! -x "$stage0" ]; then
    asset="$url/fern-selfhost-$HOST.gz"
    echo "stage0: downloading $asset"
    mkdir -p "$(dirname "$stage0")"
    curl -fsSL --retry 3 --retry-delay 2 -o "$stage0.gz" "$asset" \
      || die "download failed: $asset"
    gzip -dc "$stage0.gz" > "$stage0.tmp" || die "$asset is not gzip data"
    rm -f "$stage0.gz"
    got="$(sha256 "$stage0.tmp")"
    if [ "$got" != "$want" ]; then
      rm -f "$stage0.tmp"
      die "sha256 mismatch for $asset: lock pins $want, downloaded $got"
    fi
    chmod +x "$stage0.tmp"
    mv "$stage0.tmp" "$stage0"
  fi
  got="$(sha256 "$stage0")"
  [ "$got" = "$want" ] || die "cached $stage0 has sha256 $got, lock pins $want — delete it and re-run"
  echo "stage0: $tag for $HOST (sha256 $got)"
}

# stage NAME COMPILER: compile the compiler's own source with COMPILER into
# $OUT/NAME, for this host, and report the cost.
stage() {
  local t0 out
  out="$OUT/$1"
  rm -f "$out"
  t0=$(date +%s)
  "$2" -target "$HOST" -o "$out" "$ENTRY" "$STDLIB" \
    || die "$1 failed: $2 could not compile $ENTRY (an old stage0 meeting a construct it does not know? see docs/BOOTSTRAP.md on refreshing the pin)"
  chmod +x "$out"
  echo "$1: $(( $(date +%s) - t0 )) s, $(size "$out") bytes"
}

# smoke COMPILER: the compiler must compile a program and the result must
# run — a binary that links but cannot execute is not a compiler.
smoke() {
  local src bin code
  src="$OUT/smoke.fern"
  bin="$OUT/smoke"
  echo 'function main(): i32 { return 42; }' > "$src"
  rm -f "$bin"
  "$1" -target "$HOST" -o "$bin" "$src" "$STDLIB" || die "smoke: $1 could not compile $src"
  chmod +x "$bin"
  code=0
  "$bin" || code=$?
  [ "$code" = 42 ] || die "smoke: program compiled by $1 exited $code, want 42"
  echo "smoke: $1 compiles and its output runs"
}

build() {
  resolve_stage0
  stage stage1 "$stage0"
  smoke "$OUT/stage1"
  mkdir -p "$ROOT/bin"
  cp "$OUT/stage1" "$ROOT/bin/fern-selfhost"
  echo "installed bin/fern-selfhost ($HOST, sha256 $(sha256 "$OUT/stage1"))"
}

distcheck() {
  [ -x "$OUT/stage1" ] || build
  stage stage2 "$OUT/stage1"
  if ! cmp -s "$OUT/stage1" "$OUT/stage2"; then
    echo "bootstrap: stage1 != stage2 — the compiler does not reproduce itself" >&2
    cmp "$OUT/stage1" "$OUT/stage2" >&2 || true
    echo "  stage1: $(size "$OUT/stage1") bytes  $OUT/stage1" >&2
    echo "  stage2: $(size "$OUT/stage2") bytes  $OUT/stage2" >&2
    die "both binaries are kept; docs/BOOTSTRAP.md has the bisection recipe"
  fi
  echo "stage1 == stage2: fixed point reached (sha256 $(sha256 "$OUT/stage2"))"
}

mkdir -p "$OUT"
"$mode"
