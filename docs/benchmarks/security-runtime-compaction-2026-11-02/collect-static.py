import hashlib
import json
from pathlib import Path
import subprocess
import sys

source = Path('/Users/jakechampion/codex/lang-pr-14010-validation')
root = Path('/tmp/fern-14010-repair-v3-static-' + sys.argv[1])
root.mkdir(exist_ok=True)
engine = Path('/tmp/fern-14010-repair-v3-fixed-point/stage4')
fixed = json.loads(Path('/tmp/fern-14010-repair-v3-fixed-point/qualification.json').read_text())
assert hashlib.sha256(engine.read_bytes()).hexdigest() == fixed['stages'][-1]['sha256']
for name, digest in fixed['source_hashes'].items():
    assert hashlib.sha256((source / name).read_bytes()).hexdigest() == digest, name
names = ['int_loop', 'array_index', 'deflate_encode'] if sys.argv[1] == 'pilot' else [p.stem for p in sorted((source / 'bench').glob('*.fern'))]
record = {'scope': 'Static instruction-line counts only, from the repaired primary compiler. Original baselines remain unchanged. No dynamic or latency claim.', 'compiler_sha256': hashlib.sha256(engine.read_bytes()).hexdigest(), 'fixed_point_record_sha256': hashlib.sha256(Path('/tmp/fern-14010-repair-v3-fixed-point/qualification.json').read_bytes()).hexdigest(), 'rows': []}
for target, arch in [('arm64-linux', 'aarch64'), ('x86-64-linux', 'x86_64')]:
    baseline_path = source / '.github' / ('perf-baseline-' + arch + '.txt')
    if not baseline_path.exists():
        baseline_path = source / '.github/perf-baseline.txt'
    baseline = {}
    for line in baseline_path.read_text().splitlines():
        fields = line.split()
        if len(fields) >= 2 and not line.startswith('#'):
            baseline[fields[0]] = float(fields[1])
    record.setdefault('baseline_hashes', {})[str(baseline_path.relative_to(source))] = hashlib.sha256(baseline_path.read_bytes()).hexdigest()
    for name in names:
        command = [str(engine), '-target', target, str(source / 'bench' / (name + '.fern')), str(source / 'internal/stdlib')]
        p = subprocess.run(command, capture_output=True, timeout=120)
        assert p.returncode == 0 and p.stdout, p.stderr[-2000:]
        (root / (name + '-' + target + '.s')).write_bytes(p.stdout)
        count = sum(bool(line) and line[0].isspace() for line in p.stdout.decode().splitlines())
        key = arch + '/' + name + '.text'
        old = baseline.get(key)
        row = {'key': key, 'command': command, 'asm_sha256': hashlib.sha256(p.stdout).hexdigest(), 'instructions': count, 'baseline': old}
        record['rows'].append(row)
        (root / 'qualification.json').write_text(json.dumps(record, indent=2) + '\n')
        print(key, count, 'baseline', old, flush=True)
    (root / (arch + '.tsv')).write_text(''.join(r['key'] + '\t' + str(r['instructions']) + '\n' for r in record['rows'] if r['key'].startswith(arch + '/')))
