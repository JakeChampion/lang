#!/usr/bin/env bash
# Bombadil end-to-end harness for the Fern playground.
#
#   ./web/test/run.sh                       # local: assumes bombadil + chromium on PATH
#   BOMBADIL=/path/to/bombadil ./web/test/run.sh
#   CHROME=/path/to/chromium ./web/test/run.sh
#
# Builds the wasm bundle, starts a static HTTP server on a free
# loopback port, points Bombadil at the served page using
# web/test/playground.spec.ts, and tears the server down when done.
# Exit code is whatever Bombadil produced.
#
# Optional knobs (env vars):
#   BOMBADIL              path to the bombadil binary (defaults to `bombadil` on PATH)
#   BOMBADIL_OUTPUT       results directory (default: web/test/results)
#   BOMBADIL_TIME_LIMIT   per-run upper bound, Bombadil time format (default: 60s)
#   BOMBADIL_EXTRA_ARGS   extra flags appended verbatim to the bombadil invocation
#                         (e.g. drop --headless locally to watch the browser drive itself)
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
web="$(cd "$here/.." && pwd)"

bombadil_bin="${BOMBADIL:-bombadil}"
output_dir="${BOMBADIL_OUTPUT:-$here/results}"

if ! command -v "$bombadil_bin" >/dev/null 2>&1; then
  echo "bombadil not found on PATH (set BOMBADIL=/path/to/bombadil or install via web/test/README.md)" >&2
  exit 127
fi

mkdir -p "$output_dir"

# Build the wasm bundle so the page has something to load. build.sh
# refreshes wasm_exec.js too — important because the Go toolchain
# version determines which import-object layout the page expects.
echo "==> building playground wasm"
"$web/build.sh"

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 not found — needed to serve the playground for Bombadil" >&2
  echo "(install via apt / brew / etc. — any version since 3.7)" >&2
  exit 127
fi

# Pick a free port via Python's getsockname trick; beats hardcoding
# 8000 (which collides with other dev servers).
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("",0)); print(s.getsockname()[1]); s.close()')"

# Start a static HTTP server bound to localhost so the wasm fetch +
# the ESM imports (CodeMirror from esm.sh) work; file:// origins
# block both. --bind 127.0.0.1 keeps it off any external interface.
echo "==> starting http server on http://127.0.0.1:$port"
python3 -m http.server --bind 127.0.0.1 --directory "$web" "$port" >"$output_dir/server.log" 2>&1 &
server_pid=$!

cleanup() {
  if kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# Wait for the server to start accepting connections. ~3s budget so
# CI doesn't flake on a slow Python launch; bail loudly if the
# server never comes up so the operator sees the real cause
# instead of a confusing Bombadil connection error.
server_up=0
for _ in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:$port/index.html" -o /dev/null; then
    server_up=1
    break
  fi
  sleep 0.1
done
if [ "$server_up" -ne 1 ]; then
  echo "==> server never came up — last 20 lines of server.log:" >&2
  tail -n 20 "$output_dir/server.log" >&2 || true
  exit 1
fi

extra_args="${BOMBADIL_EXTRA_ARGS:-}"
time_limit="${BOMBADIL_TIME_LIMIT:-60s}"
echo "==> running bombadil"
# `bombadil test <ORIGIN> [SPECIFICATION_FILE]` — the spec is a
# positional argument, not a flag.
#
# Flag breakdown:
#   --headless           run Chromium without a visible window (required
#                        on CI runners without a display server)
#   --no-sandbox         Chromium's SUID sandbox helper isn't configured
#                        on most CI images (chrome-sandbox needs root +
#                        mode 4755); disable it explicitly so the
#                        browser starts at all
#   --time-limit         cap each run so CI doesn't burn minutes
#                        forever; tunable via BOMBADIL_TIME_LIMIT for
#                        nightly long-runs
#   --exit-on-violation  bail as soon as the first property fails so
#                        the developer sees the reproducer immediately
#                        instead of waiting for the time limit
#
# Local devs can drop --headless / --no-sandbox via BOMBADIL_EXTRA_ARGS
# to watch the browser drive itself.
# shellcheck disable=SC2086
"$bombadil_bin" test \
  "http://127.0.0.1:$port/index.html" \
  "$here/playground.spec.ts" \
  --output-path "$output_dir" \
  --headless \
  --no-sandbox \
  --time-limit "$time_limit" \
  --exit-on-violation \
  $extra_args
