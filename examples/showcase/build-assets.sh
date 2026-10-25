#!/usr/bin/env bash
# Compiles the showcase's browser assets with the Fern compiler named by $1:
# the Wasm kernels, and the Swarm kernel editor (the Fern compiler built
# for the browser, its WASI host, and the tangled kernel source).
set -euo pipefail
case "$1" in
  /*) fern="$1" ;;
  */*) fern="$PWD/$1" ;;
  *) fern="$(command -v "$1")" ;;
esac
cd "$(dirname "$0")"
root=../..

"$fern" -target wasm32-wasi -emit core-module -O -o web/swarm/kernel.wasm swarm.fern.md "$root/internal/stdlib"
"$fern" -target wasm32-wasi -emit core-module -O -o web/blockyard/kernel.wasm voxel.fern.md "$root/internal/stdlib"
for kernel in foundry circuit patchbay prism; do
  "$fern" -target wasm32-wasi -emit core-module -O -o "web/labs/$kernel.wasm" "$kernel.fern.md" "$root/internal/stdlib"
done

editor=web/swarm/compiler
mkdir -p "$editor"
"$fern" -target wasm32-wasi -emit core-module -embed "$root/internal/stdlib" -o "$editor/playground.wasm" \
  "$PWD/$root/compiler/playground_run.fern" "$PWD/$root/internal/stdlib"
cp "$root/web/wasi-shim.js" "$root/web/driver-worker.js" "$editor/"
"$fern" -tangle swarm.fern.md > "$editor/kernel.fern"
