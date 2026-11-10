from pathlib import Path
import datetime, gzip, hashlib, json, shutil, statistics, subprocess

repo = Path('/workspace/lang-interpreter-dispatch-20261109')
src = Path(__file__).resolve().parent
validation = repo / 'build/interpreter-retained-main-validation-20261110-0820'
dst = repo / 'docs/benchmarks/compiler-interpreter-path-control-2026-11-10'
sha = lambda data: hashlib.sha256(data).hexdigest()
result = json.loads((src / 'results.json').read_text())
v = json.loads((validation / 'validation.json').read_text())
full = json.loads((validation / 'build-full.json').read_text())
assert not dst.exists()
assert result.get('finished') and result['exit_code'] == 0 and len(result['runs']) == 36
assert all(r['exit_code'] == 0 for r in result['runs'])
assert len({r['output_sha256'] for r in result['runs']}) == 1
assert v.get('finished') and v['exit_code'] == 0
assert all(r['exit_code'] == 0 for r in v['runs'])
test = next(r for r in v['runs'] if r['label'] == 'tests')
assert test['passes'] == 413 and test['skips'] == 0
assert full.get('finished') and full['exit_code'] == 0 and full['main_binary_byte_identical']
assert full['runs'][0]['binary_sha256'] == result['binary_identities']['baseline']['sha256']
probes = [r for r in result['runs'] if r['label'].endswith('-probe')]
assert len(probes) == 2 and all(r['cache_hits'] == 112 and r['cache_misses'] == 0 for r in probes)
dst.mkdir()
compressed = {}
def retain(path, name):
    raw = path.read_bytes()
    target = dst / name
    target.parent.mkdir(parents=True, exist_ok=True)
    if any(line.endswith((b' ', b'\t', b'\r')) for line in raw.split(b'\n')) or raw.endswith(b'\n\n'):
        target = target.with_name(target.name + '.gz')
        target.write_bytes(gzip.compress(raw, mtime=0))
        compressed[str(target.relative_to(dst))] = {'original_path': str(path), 'original_sha256': sha(raw), 'original_bytes': len(raw)}
    else:
        target.write_bytes(raw)
for path in sorted(src.iterdir()):
    if path.is_file() and path.suffix in ('.py', '.json', '.stdout', '.stderr', '.log', '.patch'):
        retain(path, path.name)
for path in sorted(validation.iterdir()):
    if path.is_file() and path.suffix in ('.py', '.sh', '.json', '.log', '.stdout', '.stderr'):
        retain(path, Path('retained-main') / path.name)
retained = Path('/workspace/lang-interpreter-retained-main-20261110-0820')
audit = {'checked': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'retained_source': full['source'],
         'head': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=retained, text=True).strip(),
         'tracked_status': subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=retained, text=True),
         'compiler_and_standard_library_delta_from_main': subprocess.check_output(['git', 'diff', 'c9547605f513b81cb1c26d1cb4deaf37b20648bd', full['source'], '--', 'compiler', 'internal/stdlib'], cwd=retained, text=True)}
assert audit['head'] == full['source'] and not audit['tracked_status'] and not audit['compiler_and_standard_library_delta_from_main']
(dst / 'post-run-source-check.json').write_text(json.dumps(audit, indent=2) + '\n')
(dst / 'compression-identities.json').write_text(json.dumps(compressed, indent=2) + '\n')
(dst / 'manifest.json').write_text(json.dumps({'sha256': {str(p.relative_to(dst)): sha(p.read_bytes()) for p in sorted(dst.rglob('*')) if p.is_file() and p.name != 'manifest.json'}}, indent=2) + '\n')
print(json.dumps({'archive': str(dst), 'data_hashes': len(json.loads((dst / 'manifest.json').read_text())['sha256']), 'compressed_records': len(compressed)}))
