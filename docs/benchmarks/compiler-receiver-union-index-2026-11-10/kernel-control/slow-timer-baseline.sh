#!/bin/bash
set -euo pipefail
source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
export FERN_SELFHOST_BUILD_CACHE=/workspace/lang-checker-union-index-20261110-1540/build/driver-cache-20261110-1600
export TMPDIR=/dev/shm/fern-union-validation-20261110-1640
export PATH=/workspace/scratch/lang-qemu-system-20261110-1650/bin:$PATH
export FERN_REQUIRE_QEMU_SYSTEM=1
exec python3 /workspace/lang-checker-union-index-20261110-1540/build/ci-kernel-failure-20261110-1647/slow-timer-baseline.py
