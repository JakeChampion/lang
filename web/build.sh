#!/usr/bin/env bash
# Build the wasm bundle for the in-browser playground.
#
#   ./web/build.sh         # writes web/fern.wasm
#
# Then serve the `web/` directory with any static HTTP server,
# e.g.  `python3 -m http.server 8000 --directory web` and open
# http://localhost:8000/. Module workers fail under file://
# because the wasm fetch needs an http: origin.
#
# The runtime shim `web/wasm_exec.js` is copied from the Go
# distribution ($GOROOT/lib/wasm) so the page's
# <script src="wasm_exec.js"> works without a bundler.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here/.."

GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$here/fern.wasm" ./cmd/fern-wasm

# Refresh wasm_exec.js from the Go distribution.
goroot="$(go env GOROOT)"
if [ -f "$goroot/lib/wasm/wasm_exec.js" ]; then
  cp "$goroot/lib/wasm/wasm_exec.js" "$here/wasm_exec.js"
else
  echo "warning: couldn't find wasm_exec.js under $goroot/lib/wasm" >&2
fi

size=$(stat -c%s "$here/fern.wasm" 2>/dev/null || stat -f%z "$here/fern.wasm")
echo "wrote $here/fern.wasm  ($size bytes)"
echo "serve with:  python3 -m http.server --directory $here 8000"
