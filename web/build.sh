#!/usr/bin/env bash
# Build the wasm bundles for the in-browser playground.
#
#   ./web/build.sh         # writes web/playground.wasm and web/fern.wasm
#
# web/playground.wasm is the self-host compiler, compiled BY the self-host
# compiler to a WASI command module: source on stdin, the mode in argv
# (-check, -interp, -target, -emit), the artifact on stdout
# (compiler/playground_run.fern). The page runs it through
# web/wasi-shim.js. The compiler that builds it is bin/fern-selfhost —
# `make bootstrap` (no Go) or `make selfhost-cli` (via bin/fern) both produce
# it — or the one FERN_SELFHOST names.
#
# web/fern.wasm is the Go toolchain under GOOS=js, kept for the language
# server behind the editor's diagnostics, hover and completion, which the
# self-host compiler does not have (docs/PLAYGROUND-SELFHOST-WASM.md). Its
# runtime shim,
# web/wasm_exec.js, is copied from the Go distribution ($GOROOT/lib/wasm)
# so the page's <script src="wasm_exec.js"> works without a bundler.
#
# Then serve the `web/` directory with any static HTTP server,
# e.g.  `python3 -m http.server 8000 --directory web` and open
# http://localhost:8000/. Module workers fail under file://
# because the wasm fetch needs an http: origin.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here/.."

selfhost="${FERN_SELFHOST:-bin/fern-selfhost}"
if [ ! -x "$selfhost" ]; then
  echo "building the self-host compiler (make selfhost-cli)" >&2
  make selfhost-cli >&2
fi
"$selfhost" -target wasm32-wasi -emit core-module -embed internal/stdlib \
  -o "$here/playground.wasm" "$PWD/compiler/playground_run.fern" "$PWD/internal/stdlib"

GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$here/fern.wasm" ./cmd/fern-wasm

# Refresh wasm_exec.js from the Go distribution.
goroot="$(go env GOROOT)"
if [ -f "$goroot/lib/wasm/wasm_exec.js" ]; then
  cp "$goroot/lib/wasm/wasm_exec.js" "$here/wasm_exec.js"
else
  echo "warning: couldn't find wasm_exec.js under $goroot/lib/wasm" >&2
fi

for f in playground.wasm fern.wasm; do
  size=$(stat -c%s "$here/$f" 2>/dev/null || stat -f%z "$here/$f")
  echo "wrote $here/$f  ($size bytes)"
done
echo "serve with:  python3 -m http.server --directory $here 8000"
