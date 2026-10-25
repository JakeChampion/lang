#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
mkdir -p build
"$FERN" -check benchmarks/worker_probe.fern
FERN_LEAKCHECK=1 "$FERN" -target x86-64-linux -o build/worker-probe benchmarks/worker_probe.fern
python3 benchmarks/qualify_workers.py build/worker-probe "$(command -v "$FERN")" build/worker-qualification.json
