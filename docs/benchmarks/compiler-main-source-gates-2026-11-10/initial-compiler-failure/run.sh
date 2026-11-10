#!/bin/bash
set -eu
source /tmp/lang-performance-toolchain-20261109-1950.env
export FERN_RC_CERTIFY=1 FERN_RC_CERTIFY_MEMO=0
python3 /workspace/lang-user-net-capability-20261110-1000/build/wide-provenance-20261110-1015/collect.py
