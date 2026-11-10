#!/bin/bash
set -euo pipefail
source /tmp/lang-performance-toolchain-20261109-1950.env
exec python3 /workspace/lang-checker-union-index-20261110-1540/build/qualification-20261110-1600/opus-cold-instructions/collect.py
