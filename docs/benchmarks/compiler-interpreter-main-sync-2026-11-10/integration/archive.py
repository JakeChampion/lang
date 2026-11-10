from pathlib import Path
import datetime, gzip, hashlib, json, subprocess

repo = Path('/workspace/lang-interpreter-dispatch-20261109')
integration = Path(__file__).resolve().parent
validation = repo / 'build/interpreter-retained-main-validation-20261110-0845'
dst = repo / 'docs/benchmarks/compiler-interpreter-main-sync-2026-11-10'
sha = lambda raw: hashlib.sha256(raw).hexdigest()
v = json.loads((validation / 'validation.json').read_text())
b = json.loads((validation / 'build-full.json').read_text())
assert not dst.exists()
assert v.get('finished') and v['exit_code'] == 0 and all(r['exit_code'] == 0 for r in v['runs'])
assert b.get('finished') and b['exit_code'] == 0 and b['main_binary_byte_identical']
assert b['runs'][0]['binary_sha256'] == b['runs'][1]['binary_sha256']
assert all(r['exit_code'] == 0 for r in b['runs'])
tests = {r['label']: r for r in v['runs'] if r['label'] in ('tests', 'tls-tests')}
assert tests['tests']['passes'] == 413 and tests['tls-tests']['passes'] == 14
assert all(r['skips'] == 0 for r in tests.values())
audit = {'checked': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'main': b['main_source'], 'retained': b['source'], 'worktrees': {}}
for name, directory, expected in [('main', '/workspace/lang-interpreter-main-base-20261110-0845', b['main_source']),
                                  ('retained', '/workspace/lang-interpreter-retained-main-20261110-0845', b['source'])]:
    head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=directory, text=True).strip()
    status = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=directory, text=True)
    assert head == expected and not status
    audit['worktrees'][name] = {'path': directory, 'head': head, 'tracked_status': status}
audit['compiler_and_stdlib_delta'] = subprocess.check_output(['git', 'diff', b['main_source'], b['source'], '--', 'compiler', 'internal/stdlib'], cwd=repo, text=True)
assert not audit['compiler_and_stdlib_delta']
def rows(text):
    return [tuple(line.split('#', 1)[0].split()) for line in text.splitlines() if line.split('#', 1)[0].strip()]
for name in ['perf-baseline.txt', 'perf-baseline-selfhost.txt', 'profile-costs-baseline.txt', 'alloc-baseline.txt']:
    assert rows((repo / '.github' / name).read_text()) == rows(subprocess.check_output(['git', 'show', b['main_source'] + ':.github/' + name], cwd=repo, text=True))
audit['all_numerical_baselines_and_tolerances_match_main'] = True
dst.mkdir()
compressed = {}
def retain(path, name):
    raw = path.read_bytes()
    target = dst / name
    target.parent.mkdir(parents=True, exist_ok=True)
    if any(line.endswith((b' ', b'\t', b'\r')) for line in raw.split(b'\n')) or raw.endswith(b'\n\n') or any(line.startswith((b'<<<<<<<', b'=======', b'>>>>>>>')) for line in raw.split(b'\n')):
        target = target.with_name(target.name + '.gz')
        target.write_bytes(gzip.compress(raw, mtime=0))
        compressed[str(target.relative_to(dst))] = {'original_path': str(path), 'original_sha256': sha(raw), 'original_bytes': len(raw)}
    else:
        target.write_bytes(raw)
for path in sorted(validation.iterdir()):
    if path.is_file() and path.suffix in ('.py', '.sh', '.log', '.json', '.stdout', '.stderr'):
        retain(path, Path('validation') / path.name)
for path in sorted(integration.iterdir()):
    if path.is_file():
        retain(path, Path('integration') / path.name)
(dst / 'source-audit.json').write_text(json.dumps(audit, indent=2) + '\n')
(dst / 'compression-identities.json').write_text(json.dumps(compressed, indent=2) + '\n')
(dst / 'manifest.json').write_text(json.dumps({'sha256': {str(p.relative_to(dst)): sha(p.read_bytes()) for p in sorted(dst.rglob('*')) if p.is_file() and p.name != 'manifest.json'}}, indent=2) + '\n')
print(json.dumps({'data_hashes': len(json.loads((dst / 'manifest.json').read_text())['sha256']), 'raw_identities': len(compressed)}))
