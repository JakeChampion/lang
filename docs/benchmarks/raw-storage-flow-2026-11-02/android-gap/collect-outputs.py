import hashlib
import json
import os
from pathlib import Path
import subprocess

checkout = Path('/Users/jakechampion/codex/lang-pr-14024-validation')
limit = int(os.environ.get('FERN_OUTPUT_LIMIT', '0'))
root = Path('/tmp/fern-14024-android-gap-outputs' + (f'-{limit}' if limit else ''))
root.mkdir(exist_ok=False)
compiler = Path('/tmp/fern-14024-android-gap-fixed-point/stage4')
prior = Path('/tmp/fern-14024-main503-perf-static-arm')
qualification = json.loads(Path('/tmp/fern-14024-android-gap-fixed-point/qualification.json').read_text())
digest = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
assert digest(compiler) == qualification['stages'][-1]['sha256']
for name, value in qualification['source_hashes'].items():
    assert digest(checkout / name) == value, name
record = {'scope': 'Byte comparison with the qualified pre-Android-gap benchmark outputs. Earlier execution and cost records retain their original producer.', 'compiler_sha256': digest(compiler), 'source_hashes': qualification['source_hashes'], 'rows': []}
benches = sorted((checkout / 'bench').glob('*.fern'))
if limit:
    benches = benches[:limit]
for source in benches:
    for target in ['x86-64-linux', 'arm64-linux', 'wasm32-wasi']:
        key = source.stem + '-' + target
        for assembly in ([True, False] if target == 'arm64-linux' else [True]):
            output = root / (key + ('.asm' if assembly else ''))
            command = [str(compiler), '-target', target] + (['-emit', 'asm'] if assembly else []) + ['-o', str(output), str(source), str(checkout / 'internal/stdlib')]
            result = subprocess.run(command, cwd=checkout, capture_output=True, timeout=120)
            assert result.returncode == 0, result.stderr
            old = prior / output.name
            assert output.read_bytes() == old.read_bytes(), output.name
            record['rows'].append({'name': output.name, 'sha256': digest(output), 'prior_sha256': digest(old), 'command': command})
    print(source.stem, 'matches', flush=True)
for name, value in qualification['source_hashes'].items():
    assert digest(checkout / name) == value, name
(root / 'qualification.json').write_text(json.dumps(record, indent=2) + '\n')
print(len(record['rows']), 'identical outputs', flush=True)
