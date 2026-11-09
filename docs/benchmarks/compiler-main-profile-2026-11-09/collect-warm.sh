#!/usr/bin/env bash
set -euo pipefail
cd /workspace/lang-liveness-followup-20261109
profile_dir=/workspace/lang/build/compiler-main-profile-20261109
cpu_compiler=$profile_dir/fern-cpu
fixed_root=/workspace/lang-liveness-main-base-20261109
export FERN_PROFILE=0
export FERN_RC_CERTIFY=1
export FERN_RC_CERTIFY_MEMO=$profile_dir/cpu-memo
unset FERN_IR_VERIFY

"$cpu_compiler" -target x86-64-linux -o "$profile_dir/checker-cpu" "$fixed_root/compiler/checker.fern" "$fixed_root/internal/stdlib" > "$profile_dir/cpu-prime.stdout" 2> "$profile_dir/cpu-prime.folded"
sha256sum "$profile_dir/checker-cpu" > "$profile_dir/cpu-output.sha256"
sha256sum "$profile_dir"/cpu-memo/certified/*.txt > "$profile_dir/cpu-memo.sha256"

FERN_IR_VERIFY=1 "$cpu_compiler" -target x86-64-linux -o "$profile_dir/checker-cpu" "$fixed_root/compiler/checker.fern" "$fixed_root/internal/stdlib" > "$profile_dir/cpu-probe.stdout" 2> "$profile_dir/cpu-cache-probe.log"
sha256sum -c "$profile_dir/cpu-output.sha256"
sha256sum -c "$profile_dir/cpu-memo.sha256"
python3 - <<'PY'
import pathlib,re
p=pathlib.Path('/workspace/lang/build/compiler-main-profile-20261109/cpu-cache-probe.log')
rows=re.findall(r'FERN_RC_CERTIFY: memo hits=(\d+) misses=(\d+)',p.read_text())
assert rows, 'No memo summary'
assert sum(int(h) for h,m in rows)>0, rows
assert all(int(m)==0 for h,m in rows), rows
print('Verified cache hits with zero misses:', rows)
PY

"$cpu_compiler" -target x86-64-linux -o "$profile_dir/checker-cpu" "$fixed_root/compiler/checker.fern" "$fixed_root/internal/stdlib" > "$profile_dir/cpu-warm.stdout" 2> "$profile_dir/cpu-warm.folded"
sha256sum -c "$profile_dir/cpu-output.sha256"
sha256sum -c "$profile_dir/cpu-memo.sha256"
