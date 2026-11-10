source /tmp/lang-performance-toolchain-20261109-1950.env
export STAGE0=/workspace/lang-interpreter-timeout-20261109/build/bootstrap/stage0/stage0-20261109-a19e872/fern-selfhost-x86-64-linux
cd /workspace/lang-interpreter-dispatch-20261109 || exit 2
python3 build/interpreter-dispatch-20261109/verify.py
