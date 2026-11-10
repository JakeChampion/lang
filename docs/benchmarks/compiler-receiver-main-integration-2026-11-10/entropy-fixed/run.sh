#!/bin/bash
set -euo pipefail
source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
export FERN_SELFHOST_BUILD_CACHE=/workspace/lang-checker-union-index-20261110-1540/build/driver-cache-20261110-1600
export TMPDIR=/dev/shm/fern-union-integration-20261110-1845
export GOCACHE=/dev/shm/fern-union-gocache-20261110-1845
export PATH=/workspace/scratch/lang-qemu-system-20261110-1650/bin:$PATH
export FERN_REQUIRE_QEMU_SYSTEM=1
mkdir -p "$TMPDIR" "$GOCACHE"
exec python3 /workspace/lang-checker-union-index-20261110-1540/build/main-integration-20261110-1845/entropy-fixed/repro.py
