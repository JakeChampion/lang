import hashlib
import json
import os
from pathlib import Path
import subprocess

source = Path('/Users/jakechampion/codex/lang-pr-14010-validation')
root = Path('/tmp/fern-14010-guarded-fixed-point')
root.mkdir(exist_ok=True)
prior_path = Path('/tmp/fern-14010-repair-v3-fixed-point/qualification.json')
prior = json.loads(prior_path.read_text())
producer = Path('/tmp/fern-14010-repair-v3-fixed-point/stage4')
producer_hash = hashlib.sha256(producer.read_bytes()).hexdigest()
assert producer_hash == prior['stages'][-1]['sha256']
def hashes():
    return {str(p.relative_to(source)): hashlib.sha256(p.read_bytes()).hexdigest() for folder in ['compiler', 'internal/stdlib'] for p in sorted((source / folder).rglob('*')) if p.is_file() and (p.name.endswith('.fern') or p.name.endswith('.fern.md'))}
source_hashes = hashes()
changed = [name for name, digest in source_hashes.items() if prior['source_hashes'].get(name) != digest]
assert sorted(changed) == sorted(['compiler/ssalargepushwideisa.fern', 'compiler/ssalargepushwidemachine.fern', 'compiler/drivers/ssalargepushisas.fern', 'compiler/drivers/ssalargepushwideisa_run.fern', 'compiler/drivers/ssalargepushwidemachine_run.fern', 'compiler/drivers/ssalargepushwidemachines.fern']), changed
binary = root / 'stage1'
command = [str(producer), '-target', 'arm64-darwin', '-embed', '/tmp/fern-14008-embed', '-o', str(binary), str(source / 'compiler/fern_main.fern'), str(source / 'internal/stdlib')]
p = subprocess.run(command, env=dict(os.environ, FERN_RC_CERTIFY_MEMO='0'), capture_output=True, timeout=120)
(root / 'build.stderr').write_bytes(p.stderr)
assert p.returncode == 0, p.stderr[-3000:]
digest = hashlib.sha256(binary.read_bytes()).hexdigest()
assert digest == producer_hash, (digest, producer_hash)
assert hashes() == source_hashes
record = {'scope': 'The current complete source reproduces the prior stabilized compiler byte for byte in one iteration. This is a direct fixed-point control, not a new four-stage run. The six later source changes affect proof modules and drivers.', 'source_hashes': source_hashes, 'changed_since_prior_qualification': changed, 'prior_record_sha256': hashlib.sha256(prior_path.read_bytes()).hexdigest(), 'command': command, 'producer_sha256': producer_hash, 'sha256': digest, 'bytes': binary.stat().st_size, 'self_reproduced': True, 'test_source_hashes': {name: hashlib.sha256((source / name).read_bytes()).hexdigest() for name in ['internal/testing/e2ecompiler/self_host_stack_overflow_test.go', 'internal/testing/e2ecompiler/self_host_ssa_retained_test.go', 'internal/testing/e2ecompiler/testdata/darwin_signal_context.s']}}
(root / 'qualification.json').write_text(json.dumps(record, indent=2) + '\n')
print(digest, 'equals producer', flush=True)
