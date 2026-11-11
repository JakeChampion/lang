#!/usr/bin/env bash
# Usage: programs.sh native|wasm SOURCE...
# Checks each SOURCE, compiles it with -sanitize, and runs it. FERN_IMAGE_JOBS
# programs run at the same time (default: the number of CPUs). The output of
# each program prints as one block when the program ends. The exit status is
# 1 when any program fails. Run it from the fern-image directory.
set -euo pipefail
if [ $# -lt 2 ]; then
  echo "usage: programs.sh native|wasm SOURCE..." >&2
  exit 2
fi
mode=$1
shift
case "$mode" in
  native | wasm) ;;
  *) echo "programs.sh: unknown mode $mode" >&2; exit 2 ;;
esac
FERN=${FERN:-fern}
jobs=${FERN_IMAGE_JOBS:-$(getconf _NPROCESSORS_ONLN)}
lock=$(mktemp -d)
rmdir "$lock"
trap 'rmdir "$lock" 2>/dev/null || true' EXIT

program() {
  local source=$1 name output log status=0
  name=$(basename "$source" .fern)
  log=$(mktemp)
  (
    "$FERN" -check "$source" || exit
    if [ "$mode" = native ]; then
      output="build/$name"
      "$FERN" -target x86-64-linux -sanitize -o "$output" "$source" || exit
      "$output"
    else
      output="build/wasm-$name.wasm"
      "$FERN" -target wasm32-wasi -sanitize -o "$output" "$source" || exit
      wasmtime run --dir=. "$output"
    fi
  ) > "$log" 2>&1 || status=$?
  # mkdir is atomic, so it is the lock that keeps the blocks apart.
  until mkdir "$lock" 2>/dev/null; do sleep 0.05; done
  cat "$log"
  if [ "$status" -ne 0 ]; then
    echo "programs.sh: $source failed in $mode mode (exit $status)" >&2
  fi
  rmdir "$lock"
  rm -f "$log"
  # xargs stops at an exit status of 255, so every failure exits 1.
  [ "$status" -eq 0 ] || return 1
}
export -f program
export mode FERN lock

mkdir -p build
printf '%s\n' "$@" | xargs -P "$jobs" -n 1 bash -c 'program "$0"'
