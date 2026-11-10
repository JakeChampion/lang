from pathlib import Path
import argparse, gzip, hashlib, json, re, subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--commit')
args = parser.parse_args()
root = Path(__file__).resolve().parent
repo = Path(subprocess.check_output(['git', 'rev-parse', '--show-toplevel'], cwd=root, text=True).strip())
prefix = root.relative_to(repo)

def read(name):
    if args.commit:
        return subprocess.check_output(['git', 'show', args.commit + ':' + str(prefix / name)], cwd=repo)
    return (root / name).read_bytes()

def load(name):
    return json.loads(read(name))

manifest = load('manifest.json')['sha256']
for name, expected in manifest.items():
    assert hashlib.sha256(read(name)).hexdigest() == expected, name
compressed = load('compression-identities.json')
for name, identity in compressed.items():
    raw = gzip.decompress(read(name))
    assert len(raw) == identity['original_bytes'], name
    assert hashlib.sha256(raw).hexdigest() == identity['original_sha256'], name

def original(name):
    if name in manifest:
        return read(name)
    return gzip.decompress(read(name + '.gz'))

subsets = load('subsets/results.json')
assert len(subsets['runs']) == 20 and subsets.get('finished')
for row in subsets['runs']:
    assert row['exit_code'] == 0
    assert hashlib.sha256(original('subsets/' + row['label'] + '.stdout')).hexdigest() == row['stdout_sha256']
    if '-count-' in row['label']:
        assert row['allocs'] == row['frees'] and row['live_bytes'] == 0
for group in ('first', 'second'):
    assert len({r['stdout_sha256'] for r in subsets['runs'] if r['label'].startswith(group + '-')}) == 1
compilation = load('compilation/results.json')
assert compilation.get('finished') and len(compilation['runs']) == 16
assert all(r['exit_code'] == 0 for r in compilation['runs'])
assert len({r['output_sha256'] for r in compilation['runs']}) == 1
probes = [r for r in compilation['runs'] if r['label'].endswith('-probe')]
assert len(probes) == 2 and all(r['cache_hits'] > 0 and r['cache_misses'] == 0 for r in probes)
full = load('full/results.json')
assert full.get('finished') and len(full['runs']) == 1 and full['runs'][0]['exit_code'] == 0
assert full['runs'][0]['stdout_sha256'] == compilation['program']['stdout_sha256']
assert hashlib.sha256(original('full/full-candidate.stdout')).hexdigest() == full['runs'][0]['stdout_sha256']
instructions = load('instructions/results.json')
assert instructions.get('finished') and len(instructions['runs']) == 2
for row in instructions['runs']:
    assert row['exit_code'] == 0
    totals = re.findall(rb'^summary:\s*(\d+)\s*$', original('instructions/' + row['compiler'] + '.callgrind'), re.M)
    assert len(totals) == 1 and int(totals[0]) == row['guest_instructions']
assert instructions['runs'][0]['stdout_sha256'] == instructions['runs'][1]['stdout_sha256']
validation = load('validation/validation.json')
assert validation.get('finished') and validation['runs'][0]['exit_code'] == 0
assert validation['runs'][0]['passes'] == 388 and validation['runs'][0]['skips'] == 0
print(json.dumps({'verified_data_hashes': len(manifest), 'verified_compressed_records': len(compressed),
                  'commit': args.commit, 'output_and_control_checks': 'pass'}))
