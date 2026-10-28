from pathlib import Path
import hashlib
import json
import os
import re
import subprocess
import time

p = Path(__file__).parent
root = p.parent
producer = root / 'successor-count-candidate/stage2-current'
build_stdlib = root / 'fixed-source/internal/stdlib'
profile_stdlib = p / 'internal/stdlib'
subject = p / 'compiler/checker.fern'
sha = lambda f: hashlib.sha256(f.read_bytes()).hexdigest()
assert sha(producer) == '1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52'
env = dict(os.environ, FERN_RC_CERTIFY_MEMO=str(p / 'memo-build'), VALGRIND_LIB=str(root / 'tools/usr/libexec/valgrind'))
records = []
for name, flags in [('stage2', []), ('stage2-g', ['-g'])]:
    command = [str(producer), '-O'] + flags + ['-target', 'x86-64-linux', '-o', str(p / name), str(p / 'compiler/fern.fern'), str(build_stdlib)]
    start = time.perf_counter()
    with (p / (name + '-build.log')).open('wb') as output:
        result = subprocess.run(command, env=env, stdout=output, stderr=subprocess.STDOUT)
    record = dict(command=command, exit=result.returncode, seconds=time.perf_counter() - start, producer_sha256=sha(producer))
    if result.returncode == 0:
        record['binary_sha256'] = sha(p / name)
    records.append(record)
    (p / 'builds.json').write_text(json.dumps(records, indent=2) + '\n')
    print(json.dumps(record), flush=True)
    assert result.returncode == 0
with (p / 'stage2-g.nm').open('wb') as output:
    subprocess.run(['nm', '-n', str(p / 'stage2-g')], stdout=output, check=True)
env['FERN_RC_CERTIFY_MEMO'] = str(p / 'memo-profile')
outputs = []
for target in ['x86-64-linux', 'arm64-linux', 'wasm32-wasi']:
    output = p / ('checker-' + target + '.s')
    command = [str(p / 'stage2'), '-O', '-target', target, '-emit', 'asm', '-o', str(output), str(subject), str(profile_stdlib)]
    with (p / ('checker-' + target + '.log')).open('wb') as log:
        result = subprocess.run(command, env=env, stdout=log, stderr=subprocess.STDOUT)
    record = dict(command=command, exit=result.returncode)
    if result.returncode == 0:
        record.update(sha256=sha(output), bytes=output.stat().st_size)
    outputs.append(record)
    (p / 'outputs.json').write_text(json.dumps(outputs, indent=2) + '\n')
    assert result.returncode == 0
command = [str(root / 'tools/usr/bin/time'), '-v', str(root / 'tools/usr/bin/valgrind'), '--tool=callgrind', '--callgrind-out-file=' + str(p / 'stage2.callgrind'), str(p / 'stage2'), '-O', '-target', 'x86-64-linux', '-emit', 'asm', '-o', '/dev/null', str(subject), str(profile_stdlib)]
started = time.perf_counter()
with (p / 'stage2-profile.log').open('wb') as output:
    result = subprocess.run(command, env=env, stdout=output, stderr=subprocess.STDOUT)
record = dict(command=command, exit=result.returncode, valid_full_run=result.returncode == 0, elapsed_seconds_under_callgrind=time.perf_counter() - started, binary_sha256=sha(p / 'stage2'), common_producer_sha256=sha(producer), subject_sha256=sha(subject), subject_output_sha256=outputs[0].get('sha256'), memo_warmed=True, comparison_kind='New integration baseline. No speed gain claim against the earlier subject.')
if result.returncode == 0:
    record['instructions'] = int(re.search(r'Collected\s*:\s*(\d+)', (p / 'stage2-profile.log').read_text())[1])
(p / 'stage2-profile.json').write_text(json.dumps(record, indent=2) + '\n')
print(json.dumps(record), flush=True)
assert result.returncode == 0
subprocess.run(['python3', str(p / 'resolve-profile.py'), 'stage2'], check=True)
text = (p / 'stage2.callers.resolved').read_text()
block = next(b for b in text.split('\n\n') if re.search(r'\*.*__fern_arr_box(?:\+[^ @]+)?@', b))
record['box_calls'] = sum(int(x.replace(',', '')) for x in re.findall(r'\(([\d,]+)x\)', block))
(p / 'stage2-profile-metrics.json').write_text(json.dumps(record, indent=2) + '\n')
