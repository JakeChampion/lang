from pathlib import Path
import os, subprocess, time, hashlib, json, re, sys

p = Path(__file__).parent
root = p.parent
kind = sys.argv[1] if len(sys.argv) > 1 else 'stage2'
assert kind in ['stage2', 'stage3']
binary = p / kind
producer = root / 'pair-phi-candidate/post-merge/stage3'
baseline = root / 'pair-phi-candidate/post-merge/stage4'
subject = root / 'fixed-source/compiler/checker.fern'
stdlib = root / 'fixed-source/internal/stdlib'
env = dict(os.environ, FERN_RC_CERTIFY_MEMO=str(p / ('memo-profile-' + kind)), VALGRIND_LIB=str(root / 'tools/usr/libexec/valgrind'))
args = [str(binary), '-O', '-target', 'x86-64-linux', '-emit', 'asm']
with (p / (kind + '-warm.log')).open('wb') as f:
    r = subprocess.run(args + ['-o', str(p / (kind + '-checker.s')), str(subject), str(stdlib)], env=env, stdout=f, stderr=subprocess.STDOUT)
assert r.returncode == 0, ('warm', r.returncode)
cmd = [str(root / 'tools/usr/bin/time'), '-v', str(root / 'tools/usr/bin/valgrind'), '--tool=callgrind', '--callgrind-out-file=' + str(p / (kind + '.callgrind'))] + args + ['-o', '/dev/null', str(subject), str(stdlib)]
start = time.perf_counter()
with (p / (kind + '-profile.log')).open('wb') as f:
    r = subprocess.run(cmd, env=env, stdout=f, stderr=subprocess.STDOUT)
sha = lambda f: hashlib.sha256(f.read_bytes()).hexdigest()
record = {'command': cmd, 'artifact_generation': kind, 'comparison_kind': 'common-producer' if kind == 'stage2' else 'bootstrap-secondary', 'exit': r.returncode, 'valid_full_run': r.returncode == 0, 'elapsed_seconds_under_callgrind': time.perf_counter() - start, 'candidate_sha256': sha(binary), 'baseline_sha256': sha(baseline), 'producer_sha256': sha(producer if kind == 'stage2' else p / 'stage2'), 'subject_sha256': sha(subject), 'candidate_output_sha256': sha(p / (kind + '-checker.s')), 'baseline_output_sha256': sha(root / 'pair-phi-candidate/stage3-checker.s'), 'subject_output_bytes': (p / (kind + '-checker.s')).stat().st_size, 'note': 'Only candidate stage2 shares the baseline producer. That primary comparison prices analysis cost without the new ABI in compiler machine code. Stage3 uses candidate stage2 as producer, so it is a separate bootstrap observation.'}
if r.returncode == 0:
    record['instructions'] = int(re.search(r'Collected\s*:\s*(\d+)', (p / (kind + '-profile.log')).read_text())[1])
(p / (kind + '-profile.json')).write_text(json.dumps(record, indent=2) + '\n')
print(json.dumps(record, indent=2), flush=True)
assert r.returncode == 0
