#!/usr/bin/env bash
# Build the wasm bundle for the in-browser playground.
#
#   ./web/build.sh         # writes web/lang.wasm
#
# Then serve the `web/` directory with any static HTTP server,
# e.g.  `python3 -m http.server 8000 --directory web` and open
# http://localhost:8000/. Module workers fail under file://
# because the wasm fetch needs an http: origin.
#
# The runtime shim `web/wasm_exec.js` is copied from the Go
# distribution at the location appropriate for the toolchain
# version (Go 1.24+ moved it from $GOROOT/misc/wasm to
# $GOROOT/lib/wasm). The script copies whichever path exists
# so the page's <script src="wasm_exec.js"> works without a
# bundler.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here/.."

GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$here/lang.wasm" ./cmd/lang-wasm

# Refresh wasm_exec.js from whichever Go layout is in use.
goroot="$(go env GOROOT)"
if [ -f "$goroot/lib/wasm/wasm_exec.js" ]; then
  cp "$goroot/lib/wasm/wasm_exec.js" "$here/wasm_exec.js"
elif [ -f "$goroot/misc/wasm/wasm_exec.js" ]; then
  cp "$goroot/misc/wasm/wasm_exec.js" "$here/wasm_exec.js"
else
  echo "warning: couldn't find wasm_exec.js under $goroot" >&2
fi

size=$(stat -c%s "$here/lang.wasm" 2>/dev/null || stat -f%z "$here/lang.wasm")
echo "wrote $here/lang.wasm  ($size bytes)"
echo "serve with:  python3 -m http.server --directory $here 8000"
