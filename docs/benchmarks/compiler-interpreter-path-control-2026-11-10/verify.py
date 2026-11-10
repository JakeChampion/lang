from pathlib import Path
import argparse, gzip, hashlib, json, subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--commit')
args = parser.parse_args()
root = Path(__file__).resolve().parent
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
result = load('results.json')
assert result.get('finished') and result['exit_code'] == 0 and len(result['runs']) == 36
assert all(r['exit_code'] == 0 for r in result['runs'])
assert len({r['output_sha256'] for r in result['runs']}) == 1
assert len(result['binary_identities']['baseline']['path']) == len(result['binary_identities']['candidate']['path'])
for mode in ('cold', 'warm'):
    rows = [r for r in result['runs'] if r['label'].startswith(mode + '-')]
    assert len(rows) == 16 and all(sum(r['compiler'] == n for r in rows) == 8 for n in ('baseline', 'candidate'))
probes = [r for r in result['runs'] if r['label'].endswith('-probe')]
assert len(probes) == 2 and all(r['cache_hits'] == 112 and r['cache_misses'] == 0 for r in probes)
validation = load('retained-main/validation.json')
assert validation['exit_code'] == 0 and all(r['exit_code'] == 0 for r in validation['runs'])
tests = next(r for r in validation['runs'] if r['label'] == 'tests')
assert tests['passes'] == 413 and tests['skips'] == 0
full = load('retained-main/build-full.json')
assert full['exit_code'] == 0 and full.get('finished') and full['main_binary_byte_identical']
assert full['runs'][0]['binary_sha256'] == result['binary_identities']['baseline']['sha256']
assert full['runs'][1]['exit_code'] == 0 and full['runs'][1]['stdout_sha256'] == '07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
assert not load('post-run-source-check.json')['compiler_and_standard_library_delta_from_main']
print(json.dumps({'verified_data_hashes': len(manifest), 'verified_raw_identities': len(compressed), 'commit': args.commit, 'controls': 'pass'}))
