#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
FERN=${FERN:-"$PWD/bin/fern"}
reference="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/jxl-headers.XXXXXX")"
uv run --no-project python - "$reference/source" <<'PY'
import hashlib
from pathlib import Path
import sys
import urllib.request

sys.path.insert(0, 'fern-image/tests')
from qualify_jxl_headers import UPSTREAM

root = Path(sys.argv[1])
root.mkdir()
for relative, expected in UPSTREAM.items():
    if relative.startswith('highway/'):
        url = 'https://raw.githubusercontent.com/google/highway/1.4.0/' + relative.removeprefix('highway/')
    else:
        url = 'https://raw.githubusercontent.com/libjxl/libjxl/v0.12.0/' + relative
    data = urllib.request.urlopen(url, timeout=30).read()
    if hashlib.sha256(data).hexdigest() != expected:
        raise ValueError('offline reference source hash differs: ' + relative)
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
PY
uv run --no-project python fern-image/tests/qualify_jxl_headers.py \
  --fern "$FERN" --target x86-64-linux --reference-source "$reference/source" --output "$reference/native"
uv run --no-project python fern-image/tests/qualify_jxl_headers.py \
  --fern "$FERN" --target wasm32-wasi --reference-source "$reference/source" --output "$reference/wasm" \
  --runner wasmtime --runner run --runner=--dir=.
