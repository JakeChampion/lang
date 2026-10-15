#!/usr/bin/env bash
# Build the wasm bundle for the in-browser playground.
#
#   ./web/build.sh         # writes web/playground.wasm
#
# web/playground.wasm is the self-host compiler, compiled BY the self-host
# compiler to a WASI command module: source on stdin, the mode in argv
# (-check, -interp, -lsp, -target, -emit), the artifact on stdout
# (compiler/playground_run.fern). The page runs it through
# web/wasi-shim.js, the language server behind the editor's diagnostics,
# hover and completion included. The compiler that builds it is
# bin/fern-selfhost — `make bootstrap` (no Go) or `make selfhost-cli` (via
# bin/fern) both produce it — or the one FERN_SELFHOST names.
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

size=$(stat -c%s "$here/playground.wasm" 2>/dev/null || stat -f%z "$here/playground.wasm")
echo "wrote $here/playground.wasm  ($size bytes)"
echo "serve with:  python3 -m http.server --directory $here 8000"
