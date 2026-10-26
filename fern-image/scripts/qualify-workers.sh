#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
FERN=${FERN:-fern}
mkdir -p build
python3 -m unittest discover -s tests -p qualify_workers_test.py
"$FERN" -check benchmarks/worker_probe.fern
FERN_LEAKCHECK=1 "$FERN" -target x86-64-linux -o build/worker-probe benchmarks/worker_probe.fern
python3 benchmarks/qualify_workers.py build/worker-probe "$(command -v "$FERN")" build/worker-qualification.json
