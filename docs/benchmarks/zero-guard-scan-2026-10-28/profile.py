from pathlib import Path
import os, subprocess, json, hashlib, time, re

p = Path(__file__).parent
root = p.parent
candidate = p / 'stage2'
producer = root / 'record-pair-candidate/stage3'
baseline = root / 'record-pair-candidate/stage4'
subject = root / 'fixed-source/compiler/checker.fern'
stdlib = root / 'fixed-source/internal/stdlib'
sha = lambda f: hashlib.sha256(f.read_bytes()).hexdigest()
assert sha(baseline) == sha(producer) == '64f876ff80e4db91d1eb614605a7fe567bbad13ecb061dce204defb5c0b975a0'
env = dict(os.environ, FERN_RC_CERTIFY_MEMO=str(p / 'memo-profile'), VALGRIND_LIB=str(root / 'tools/usr/libexec/valgrind'))
identities = []
for target in ['x86-64-linux', 'arm64-linux', 'wasm32-wasi']:
    outputs = []
    for label, binary in [('after', candidate)]:
        output = p / (label + '-' + target + '.s')
        cmd = [str(binary), '-O', '-target', target, '-emit', 'asm', '-o', str(output), str(subject), str(stdlib)]
        with (p / (label + '-' + target + '-output.log')).open('wb') as f:
            result = subprocess.run(cmd, env=env, stdout=f, stderr=subprocess.STDOUT)
        assert result.returncode == 0, (label, target, result.returncode)
        outputs.append(output)
    prior = root / 'edge-check-candidate' / ('before-' + target + '.s')
    outputs.insert(0, prior)
    identity = outputs[0].read_bytes() == outputs[1].read_bytes()
    identities.append({'target': target, 'byte_identical': identity, 'sha256': sha(outputs[0]), 'bytes': outputs[0].stat().st_size})
    (p / 'output-identities.json').write_text(json.dumps(identities, indent=2) + '\n')
    assert identity, target
cmd = [str(root / 'tools/usr/bin/time'), '-v', str(root / 'tools/usr/bin/valgrind'), '--tool=callgrind', '--callgrind-out-file=' + str(p / 'stage2.callgrind'), str(candidate), '-O', '-target', 'x86-64-linux', '-emit', 'asm', '-o', '/dev/null', str(subject), str(stdlib)]
start = time.perf_counter()
with (p / 'stage2-profile.log').open('wb') as f:
    result = subprocess.run(cmd, env=env, stdout=f, stderr=subprocess.STDOUT)
record = {'command': cmd, 'exit': result.returncode, 'valid_full_run': result.returncode == 0, 'baseline_sha256': sha(baseline), 'candidate_sha256': sha(candidate), 'common_producer_sha256': sha(producer), 'baseline_profile': str(root / 'record-pair-candidate/stage3-profile.json'), 'baseline_profile_binary_identical_to_stage2_baseline': True, 'baseline_output_reused_from': str(root / 'edge-check-candidate/output-identities.json'), 'elapsed_seconds_under_callgrind': time.perf_counter() - start, 'subject_sha256': sha(subject)}
if result.returncode == 0:
    record['instructions'] = int(re.search(r'Collected\s*:\s*(\d+)', (p / 'stage2-profile.log').read_text())[1])
(p / 'stage2-profile.json').write_text(json.dumps(record, indent=2) + '\n')
print(json.dumps(record, indent=2), flush=True)
assert result.returncode == 0
