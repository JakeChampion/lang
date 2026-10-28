from pathlib import Path
import subprocess, os, time, hashlib, json

p = Path(__file__).parent
assert 'PASS\nok ' in (p / 'primary-gates.log').read_text()
records = []
for producer, output in [('stage2', 'stage3'), ('stage3', 'stage4')]:
    cmd = [str(p / producer), '-O', '-target', 'x86-64-linux', '-o', str(p / output), str(p / 'compiler/fern.fern'), str(p.parent / 'fixed-source/internal/stdlib')]
    env = dict(os.environ)
    env['FERN_RC_CERTIFY_MEMO'] = str(p / ('certifier-memo-' + output))
    start = time.perf_counter()
    with (p / (output + '-build.log')).open('wb') as f:
        r = subprocess.run(cmd, env=env, stdout=f, stderr=subprocess.STDOUT)
    record = {'producer': producer, 'producer_sha256': hashlib.sha256((p / producer).read_bytes()).hexdigest(), 'command': cmd, 'output': output, 'exit': r.returncode, 'elapsed_seconds': time.perf_counter() - start}
    if r.returncode == 0:
        record['output_sha256'] = hashlib.sha256((p / output).read_bytes()).hexdigest()
        record['output_bytes'] = (p / output).stat().st_size
    records.append(record)
    (p / 'fixpoint-builds.json').write_text(json.dumps(records, indent=2) + '\n')
    print(record, flush=True)
    assert r.returncode == 0
equal = (p / 'stage3').read_bytes() == (p / 'stage4').read_bytes()
(p / 'fixpoint-result.json').write_text(json.dumps({'stage3_equals_stage4': equal, 'source': str(p / 'compiler'), 'primary_e2e_completed_first': True, 'note': 'The common producer is pair-phi-candidate/post-merge/stage3. Candidate stage2 implements record pair returns but its own code retains the prior ABI. Candidate stage3 first uses the record pair ABI for its own machine code. Stage4 tests self-reproduction. These build times are telemetry, not A/B performance data.'}, indent=2) + '\n')
assert equal, 'compiler does not reproduce itself'
