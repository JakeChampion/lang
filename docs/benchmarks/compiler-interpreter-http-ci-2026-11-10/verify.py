from pathlib import Path
import argparse
import gzip
import hashlib
import json
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--commit')
args = parser.parse_args()
root = Path(__file__).resolve().parent
repo = root.parents[2]
relative = root.relative_to(repo)
def read(name):
    if args.commit:
        return subprocess.check_output(['git', 'show', args.commit + ':' + str(relative / name)], cwd=repo)
    return (root / name).read_bytes()
manifest = json.loads(read('manifest.json'))['sha256']
for name, expected in manifest.items():
    assert hashlib.sha256(read(name)).hexdigest() == expected, name
raw = json.loads(read('raw-identities.json'))
for name, expected in raw.items():
    value = gzip.decompress(read(name))
    assert len(value) == expected['bytes'], name
    assert hashlib.sha256(value).hexdigest() == expected['sha256'], name
r = json.loads(read('result.json'))
assert r.get('finished') and len(r['pairs']) == 9 and len(r['runs']) == 43
assert len(r['verified_ci_rows']) == 24
compiles = [row for row in r['runs'] if '-target' in row['args']]
assert len(compiles) == 34 and all(row['exit_code'] == 0 for row in compiles)
assert all(row['exit_code'] == 30 for row in r['runs'] if row not in compiles)
for pair in r['pairs']:
    base, candidate = pair['files']['base'], pair['files']['candidate']
    assert base['assembly_sha256'] == candidate['assembly_sha256']
    if 'native_sha256' in base:
        assert base['native_sha256'] == candidate['native_sha256']
assert json.loads(read('controls/perf-gate-selftest-result.json'))['exit_code'] == 0
def rows(data):
    return {p[0]: p[1:] for line in data.decode().splitlines()
            if (p := line.split()) and not p[0].startswith('#')}
actual_changes = set()
for name in ['perf-baseline.txt', 'perf-baseline-selfhost.txt', 'profile-costs-baseline.txt']:
    before, after = rows(read('baselines/' + name + '.before.txt')), rows(read('baselines/' + name + '.after.txt'))
    assert before.keys() == after.keys()
    for metric in before:
        assert before[metric][1:] == after[metric][1:], metric
        if before[metric][0] != after[metric][0]:
            actual_changes.add((metric, int(before[metric][0]), int(after[metric][0])))
assert actual_changes == {(row['metric'], row['old'], row['measured']) for row in r['verified_ci_rows']}
print(json.dumps({'data_hashes_verified': len(manifest), 'raw_identities_verified': len(raw), 'baseline_rows_verified': len(actual_changes)}))
