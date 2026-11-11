#!/usr/bin/env python3
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import re
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--commit', default='HEAD')
parser.add_argument('--working-tree', action='store_true')
args = parser.parse_args()
root = Path(__file__).resolve().parents[3]
archive = 'docs/benchmarks/compiler-driver-size-ci-2026-11-11'

def git(*parts):
    return subprocess.check_output(['git', *parts], cwd=root)

def read(rel):
    if args.working_tree:
        return (root / rel).read_bytes()
    return git('show', args.commit + ':' + rel)

def data(name):
    return read(archive + '/' + name)

def digest(value):
    return hashlib.sha256(value).hexdigest()

manifest = json.loads(data('manifest.json'))['data_sha256']
for name, wanted in manifest.items():
    assert digest(data(name)) == wanted, name
identity = json.loads(data('source-identities.json'))
assert identity['measured_pr_head'] == '75dbb650647f3e97300c6dc2c9f3f734892f9489'
assert identity['product_source'] == '4e38d84c7fe34200e300ad55c05ed92d8b67c6b7'
assert identity['ci_job'] == 130515952623
for rel, wanted in identity['product_trees'].items():
    assert git('rev-parse', args.commit + ':' + rel).decode().strip() == wanted, rel
raw = {}
for name, expected in identity['raw_identities'].items():
    raw[name] = gzip.decompress(data(name))
    assert len(raw[name]) == expected['bytes'], name
    assert digest(raw[name]) == expected['sha256'], name
    assert digest(data(name)) == expected['gzip_sha256'], name
for rel, expected in identity['protocol'].items():
    value = read(rel)
    assert value == raw[expected['file']], rel
    assert digest(value) == expected['sha256'], rel
    assert hashlib.sha1(b'blob ' + str(len(value)).encode() + b'\0' + value).hexdigest() == expected['git_blob'], rel
assert digest(read('.github/selfhost-driver-sizes.txt')) == identity['publication_baseline_sha256']
assert git('rev-parse', args.commit + ':internal/testing/e2ecompiler/self_host_feature_census_test.go').decode().strip() == identity['feature_test_blob']
origin = json.loads(data('job-origin.json'))
assert digest(raw['ci-driver.log.gz']) == origin['tool_utf8_sha256'] == origin['sha256']
measured = {}
for line in raw['ci-driver.log.gz'].decode().splitlines():
    match = re.fullmatch(r'\S+Z ((?:drivers/[^\t ]+|fern\.fern(?:/stage2)?))\t([0-9]+)', line)
    if match:
        key, value = match.group(1), int(match.group(2))
        assert key not in measured or measured[key] == value
        measured[key] = value
report = {key: int(value) for key, value in (line.split() for line in data('report.tsv').decode().splitlines())}
assert measured == report and len(report) == 14

def baseline(value):
    return {parts[0]: int(parts[1]) for line in value.decode().splitlines()
            if line.strip() and not line.lstrip().startswith('#') for parts in [line.split()]}

old = baseline(raw['old-baseline.txt.gz'])
new = baseline(raw['updated-baseline.txt.gz'])
assert set(old) == set(new) == set(report)
changes = {key: (old[key], new[key]) for key in old if old[key] != new[key]}
assert changes == {
    'drivers/checker_codes_run.fern': (3663328, 3851760),
    'drivers/checker_modload_run.fern': (3913968, 4147040),
    'drivers/asm_run.fern': (9577776, 10091776),
    'drivers/asm_ir_run.fern': (10449984, 11028400),
}
assert read('.github/selfhost-driver-sizes.txt') == raw['updated-baseline.txt.gz']
for key, (_, value) in changes.items():
    assert report[key] == value
environment = json.loads(data('replay-environment.json'))
assert environment['FERN_CI_SIZE_GATE_STRICT'] == '1'
assert environment['FERN_SIZE_REPORT_COMPLETE'] == '1'
assert environment['FERN_SIZE_TOLERANCE_PERCENT'] == '5'
assert b"FERN_CI_SIZE_GATE_STRICT='1',FERN_SIZE_REPORT_COMPLETE='1',FERN_SIZE_TOLERANCE_PERCENT='5'" in raw['replay-driver-bank.py.gz']
replay = json.loads(data('strict-replay.json'))
assert replay['exit_code'] == 0
assert [(run['label'], run['exit_code'], run['expected']) for run in replay['runs']] == [('old', 1, 1), ('updated', 0, 0)]
for label in ['old', 'updated']:
    text = raw['strict-' + label + '.stdout.gz'].decode()
    assert '14 of 14 baselined drivers measured' in text
    assert 'MISSING:' not in text and 'UNBASELINED:' not in text
assert raw['strict-old.stdout.gz'].count(b'GREW:') == 4
assert b'Every driver measured here is within 5%' in raw['strict-updated.stdout.gz']
controls = json.loads(data('local-controls.json'))
assert controls['validation_resume_exit'] == 0
assert controls['feature_census']['arrow_lambdas'] == 20
assert controls['resource_census']['fixtures'] == 1339
assert controls['resource_census']['leaking_fixtures'] == 12
assert controls['resource_census']['unpaired_allocations'] == 34
assert b'perf-gate-selftest: 22 cases pass' in raw['ci-selftest.stdout.gz']
assert b'arrow lambdas                  20' in raw['feature-census.stdout.gz']
assert b'1339 fixtures measured, 12 leak, 34 unpaired allocs total' in raw['resource-census.stdout.gz']
print(json.dumps({'verified_data': len(manifest), 'lossless_gzip': len(raw), 'measured_drivers': len(report), 'strict_old_exit': 1, 'strict_updated_exit': 0, 'compiler_workloads_executed': 0}))
