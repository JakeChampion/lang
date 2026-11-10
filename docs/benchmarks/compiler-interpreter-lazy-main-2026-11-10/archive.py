from pathlib import Path
import datetime, gzip, hashlib, json, re, statistics, subprocess

repo = Path('/workspace/lang-interpreter-dispatch-20261109')
src = Path(__file__).resolve().parent
dst = repo / 'docs/benchmarks/compiler-interpreter-lazy-main-2026-11-10'
fixed = Path('/workspace/lang-interpreter-timeout-20261109')
sha = lambda data: hashlib.sha256(data).hexdigest()
load = lambda name: json.loads((src / name).read_text())
assert not dst.exists(), 'Never overwrite an archive.'
prep = load('prepare.json')
validation = load('validation/validation.json')
collector = load('collector-result.json')
assert collector.get('finished') and collector['exit_code'] == 0
assert prep.get('finished') and all(r['exit_code'] == 0 for r in prep['runs'])
assert validation.get('finished') and all(r['exit_code'] == 0 for r in validation['runs'])
test = next(r for r in validation['runs'] if r['label'] == 'tests')
assert test['passes'] == 413 and test['skips'] == 0
assert not re.search(r'^\s*--- (FAIL|SKIP):', (src / 'validation/tests.log').read_text(), re.M)
integration = repo / 'build/interpreter-main-integration-20261110-0710'
tls = json.loads((integration / 'tls-control-result.json').read_text())
assert tls.get('finished') and tls['phase'] == 'complete'
assert tls['exit_code'] == 0 and tls['skips'] == 0
compiler_work = load('compilation-instructions-v2/results.json')
assert compiler_work.get('finished') and compiler_work['exit_code'] == 0
assert len(compiler_work['runs']) == 2 and all(r['exit_code'] == 0 for r in compiler_work['runs'])
paired, compilation, full, instructions = [load(n + '/results.json') for n in ('subsets', 'compilation', 'full', 'instructions')]
for result in (paired, compilation, full, instructions):
    assert result.get('finished') and not result.get('collector_error')
    assert result['baseline_source'] == prep['baseline_source']
    assert result['candidate_source'] == prep['candidate_source']
    assert all(r['exit_code'] == 0 for r in result['runs'])
assert len(paired['runs']) == 20 and len(compilation['runs']) == 16
assert len(full['runs']) == 1 and len(instructions['runs']) == 2
for group in ('first', 'second'):
    rows = [r for r in paired['runs'] if r['label'].startswith(group + '-')]
    assert len({r['stdout_sha256'] for r in rows}) == 1
    for r in rows:
        if '-count-' in r['label']:
            assert r['allocs'] == r['frees'] and r['live_bytes'] == 0
assert len({r['output_sha256'] for r in compilation['runs']}) == 1
assert compilation['program']['exit_code'] == 0
assert compilation['program']['stdout_sha256'] == full['runs'][0]['stdout_sha256']
assert len({r['stdout_sha256'] for r in instructions['runs']}) == 1
probes = [r for r in compilation['runs'] if r['label'].endswith('-probe')]
assert len(probes) == 2 and all(r['cache_hits'] > 0 and r['cache_misses'] == 0 for r in probes)
audit = {'checked': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'worktrees': {}}
for label, path, expected in [
    ('baseline', '/workspace/lang-interpreter-main-base-20261110-0710', prep['baseline_source']),
    ('candidate', '/workspace/lang-interpreter-qualified-20261110-0710', prep['candidate_source']),
    ('fixed_input', str(fixed), 'ebb50c78c0ca714180704352881da7e39efb570f'),
]:
    head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=path, text=True).strip()
    status = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no'], cwd=path, text=True)
    assert head == expected and not status
    audit['worktrees'][label] = {'path': path, 'head': head, 'tracked_status': status}
for label, identity in prep['binaries'].items():
    assert sha((src / label).read_bytes()) == identity['sha256']
audit['interpreter_matches_original_lazy_source'] = not subprocess.check_output(
    ['git', 'diff', '597b6e782f5d2825baf3ed07dcf9eeba3dc57be2', prep['candidate_source'], '--', 'compiler/interp.fern'], cwd=repo)
assert audit['interpreter_matches_original_lazy_source']
dst.mkdir()
compressed = {}
def retain(source, target):
    raw = source.read_bytes()
    path = dst / target
    path.parent.mkdir(parents=True, exist_ok=True)
    if any(line.endswith((b' ', b'\t', b'\r')) for line in raw.split(b'\n')):
        path = path.with_name(path.name + '.gz')
        path.write_bytes(gzip.compress(raw, mtime=0))
        compressed[str(path.relative_to(dst))] = {'original_path': str(source), 'original_sha256': sha(raw), 'original_bytes': len(raw)}
    else:
        path.write_bytes(raw)
for path in sorted(src.iterdir()):
    if path.is_file() and path.suffix in ('.py', '.sh', '.json', '.log', '.stdout', '.stderr'):
        retain(path, path.name)
for mode in ('validation', 'subsets', 'compilation', 'full', 'instructions', 'compilation-instructions', 'compilation-instructions-v2'):
    for path in sorted((src / mode).iterdir()):
        if path.is_file() and path.suffix in ('.py', '.json', '.stdout', '.stderr', '.log', '.callgrind'):
            retain(path, Path(mode) / path.name)
for name in ('first-group.fern', 'second-group.fern'):
    retain(fixed / 'build/interpreter-profile-20261109' / name, Path('inputs') / (name + '.txt'))
for name in ('opus_delimited_test.fern', 'opus_delimited_vectors.fern'):
    retain(fixed / 'tests/stdlib' / name, Path('inputs') / (name + '.txt'))
retain(repo / 'build/interpreter-lazy-qualification-20261110-0345/semantic-audit.json', 'original-lazy-semantic-audit.json')
for name in ('heads-before.json', 'resolution.json', 'push-preflight.json', 'staged-diff-check.log', 'job-129681122122.log', 'job-129681122027.log', 'ci-log-origin-check.json',
             'tls-control.py', 'tls-control-result.json', 'tls-control-tests.log', 'tls-control-controller.log'):
    retain(integration / name, Path('integration') / name)
patch = subprocess.check_output(['git', 'diff', prep['baseline_source'], prep['candidate_source'], '--', 'compiler', 'internal/testing/e2ecompiler'], cwd=repo)
(dst / 'implementation.patch.gz').write_bytes(gzip.compress(patch, mtime=0))
compressed['implementation.patch.gz'] = {'original_sha256': sha(patch), 'original_bytes': len(patch)}
def dump(name, data):
    (dst / name).write_text(json.dumps(data, indent=2) + '\n')
def stats(rows):
    return {'samples': len(rows), 'wall_median_s': statistics.median(r['wall_ns'] / 1e9 for r in rows),
            'wall_range_s': [min(r['wall_ns'] / 1e9 for r in rows), max(r['wall_ns'] / 1e9 for r in rows)],
            'cpu_median_s': statistics.median(r['cpu_user'] + r['cpu_system'] for r in rows),
            'peak_rss_median_kib': statistics.median(r['peak_rss_kib'] for r in rows)}
summary = {}
for group, count in [('first', 5), ('second', 3)]:
    summary[group] = {name: stats([r for r in paired['runs'] if r['compiler'] == name and r['label'].startswith(group + '-') and '-count-' not in r['label']]) for name in ('baseline', 'candidate')}
    changes = []
    for i in range(count):
        pair = {r['compiler']: r for r in paired['runs'] if r['label'].startswith(f'{group}-{i}-')}
        changes.append(((pair['candidate']['cpu_user'] + pair['candidate']['cpu_system']) / (pair['baseline']['cpu_user'] + pair['baseline']['cpu_system']) - 1) * 100)
    summary[group]['paired_cpu_change_percent'] = changes
    summary[group]['allocations'] = {r['compiler']: {k: r[k] for k in ('allocs', 'frees', 'live_bytes')} for r in paired['runs'] if r['label'].startswith(group + '-count-')}
for mode in ('cold', 'warm'):
    summary[mode] = {name: stats([r for r in compilation['runs'] if r['compiler'] == name and r['label'].startswith(mode + '-')]) for name in ('baseline', 'candidate')}
    changes = []
    for i in range(3):
        pair = {r['compiler']: r for r in compilation['runs'] if r['label'].startswith(f'{mode}-{i}-')}
        changes.append(((pair['candidate']['cpu_user'] + pair['candidate']['cpu_system']) / (pair['baseline']['cpu_user'] + pair['baseline']['cpu_system']) - 1) * 100)
    summary[mode]['paired_cpu_change_percent'] = changes
summary.update(full=full['runs'][0], program=compilation['program'], cache_probes=probes, instructions=instructions, compiler_work=compiler_work)
dump('summary.json', summary)
dump('post-run-source-check.json', audit)
dump('compression-identities.json', compressed)
dump('manifest.json', {'sha256': {str(p.relative_to(dst)): sha(p.read_bytes()) for p in sorted(dst.rglob('*')) if p.is_file() and p.name != 'manifest.json'}})
print(json.dumps({'archive': str(dst), 'data_hashes': len(json.loads((dst / 'manifest.json').read_text())['sha256']), 'compressed_records': len(compressed)}))
