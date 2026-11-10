from pathlib import Path
import argparse, gzip, hashlib, json, subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--commit')
args = parser.parse_args()
root = Path(__file__).resolve().parent
if root.name == 'integration':
    root = root.parent
repo = Path(subprocess.check_output(['git', 'rev-parse', '--show-toplevel'], cwd=root, text=True).strip())
prefix = root.relative_to(repo)
def read(name):
    return subprocess.check_output(['git', 'show', args.commit + ':' + str(prefix / name)], cwd=repo) if args.commit else (root / name).read_bytes()
def load(name):
    return json.loads(read(name))
manifest = load('manifest.json')['sha256']
for name, expected in manifest.items():
    assert hashlib.sha256(read(name)).hexdigest() == expected, name
compressed = load('compression-identities.json')
for name, identity in compressed.items():
    raw = gzip.decompress(read(name))
    assert len(raw) == identity['original_bytes'] and hashlib.sha256(raw).hexdigest() == identity['original_sha256']
v, b = load('validation/validation.json'), load('validation/build-full.json')
assert v.get('finished') and v['exit_code'] == 0 and all(r['exit_code'] == 0 for r in v['runs'])
tests = {r['label']: r for r in v['runs'] if r['label'] in ('tests', 'tls-tests')}
assert tests['tests']['passes'] == 413 and tests['tls-tests']['passes'] == 14
assert all(r['skips'] == 0 for r in tests.values())
assert b.get('finished') and b['exit_code'] == 0 and all(r['exit_code'] == 0 for r in b['runs'])
assert b['main_binary_byte_identical'] and b['runs'][0]['binary_sha256'] == b['runs'][1]['binary_sha256']
assert b['runs'][2]['stdout_sha256'] == '07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
audit = load('source-audit.json')
assert not audit['compiler_and_stdlib_delta'] and audit['all_numerical_baselines_and_tolerances_match_main']
print(json.dumps({'verified_data_hashes': len(manifest), 'verified_raw_identities': len(compressed), 'commit': args.commit, 'controls': 'pass'}))
