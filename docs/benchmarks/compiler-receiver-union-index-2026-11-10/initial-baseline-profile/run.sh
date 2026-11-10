#!/bin/bash
set -eu
source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
python3 /workspace/lang-checker-union-index-20261110-1540/build/baseline-profile-20261110-1540/collect.py
