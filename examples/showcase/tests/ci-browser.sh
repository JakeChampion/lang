#!/usr/bin/env bash
# Builds the showcase from this checkout, serves it, and runs every browser
# suite against it. The Showcase CI lane runs this script.
#
# Set FERN to use a compiler that is already built. Set CHROMIUM to the
# browser executable; the default is the one Playwright installed.
set -euo pipefail
cd "$(dirname "$0")/../../.."

fern="${FERN:-$PWD/bin/fern}"
if [ ! -x "$fern" ]; then
  make build
fi
out="$(mktemp -d)"

echo "==> compiling the server and the browser assets"
"$fern" -target x86-64-linux -O -o "$out/showcase" examples/showcase/server.fern.md
examples/showcase/build-assets.sh "$fern"

echo "==> preparing assets"
mkdir -p "$out/root"
ln -s "$PWD/examples/fractal" "$out/root/fractal"
node examples/showcase/prepare.mjs "$out/root/showcase" "$out/showcase"

"$out/showcase" "$out/root" > "$out/server.log" 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true' EXIT
for _ in $(seq 120); do
  curl -sf -o /dev/null http://localhost:8080/healthz && break
  sleep 0.5
done

export CHROMIUM="${CHROMIUM:-$(cd examples/showcase/tests && node -e 'import("playwright").then((p) => console.log(p.chromium.executablePath()))')}"
for suite in theme.browser browser interaction.browser labs.browser blockyard.browser editor.browser bench.browser; do
  echo "==> $suite"
  if ! node "examples/showcase/tests/$suite.mjs" http://localhost:8080; then
    echo "--- server log" >&2
    tail -50 "$out/server.log" >&2
    exit 1
  fi
done
