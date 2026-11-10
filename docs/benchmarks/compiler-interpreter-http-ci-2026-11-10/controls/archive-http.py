from pathlib import Path
import gzip
import hashlib
import json
import re
import shutil
import subprocess

repo = Path('/workspace/lang-interpreter-dispatch-20261109')
control = Path(__file__).resolve().parent
data = repo / 'build/interpreter-http-baselines-20261110-0528'
prior = repo / 'build/interpreter-http-baselines-20261110-0520'
archive = repo / 'docs/benchmarks/compiler-interpreter-http-ci-2026-11-10'
state = json.loads((data / 'result.json').read_text())
assert state.get('finished') and len(state['verified_ci_rows']) == 24
assert all(r.get('finished') for r in state['runs'])
assert json.loads((control / 'perf-gate-selftest-result.json').read_text())['exit_code'] == 0
assert not subprocess.check_output(['git', 'status', '--porcelain'], cwd=repo)
assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip() == '36d1f7b521f58c90c9fcd79aafe9326677d82850'
archive.mkdir(exist_ok=False)
sha = lambda b: hashlib.sha256(b).hexdigest()
raw = {}
def copy(src, dest):
    target = archive / dest
    target.parent.mkdir(parents=True, exist_ok=True)
    content = src.read_bytes()
    if dest.endswith('.gz'):
        target.write_bytes(gzip.compress(content, mtime=0))
        raw[dest] = {'sha256': sha(content), 'bytes': len(content)}
        assert gzip.decompress(target.read_bytes()) == content
    else:
        target.write_bytes(content)
for name in ['reproduce-http.py', 'resume-http.py', 'archive-http.py',
             'reproduce-http.log', 'resume-http.log', 'perf-gate-selftest-result.json',
             'failed-perf-job.json', 'failed-perf-arm-job.json']:
    copy(control / name, 'controls/' + name)
for name in ['perf-x86-129627638421.log', 'perf-arm-129627638638.log', 'perf-gate-selftest.log']:
    copy(control / name, 'ci/' + name + '.gz')
copy(prior / 'result.json', 'initial-result.json')
copy(data / 'result.json', 'result.json')
for file in sorted(data.iterdir()):
    if not file.is_file() or file.name == 'result.json' or file.suffix == '.bin':
        continue
    dest = 'data/' + file.name
    if file.suffix in ['.s', '.callgrind', '.log']:
        dest += '.gz'
    copy(file, dest)
for name in ['perf-bench', 'perf-bench-selfhost', 'profile-costs', 'ci-check-perf', 'perf-gate-selftest.mjs']:
    copy(repo / 'scripts' / name, 'scripts/' + name + '.txt')
fixed = Path('/workspace/lang-interpreter-map-main-base-20261110-0330')
copy(fixed / 'bench/http_hello.fern', 'http_hello.fern.txt')
copy(fixed / 'bootstrap/stage0.lock', 'stage0.lock')
(archive / 'raw-identities.json').write_text(json.dumps(raw, indent=2) + '\n')
patch = ['*** Begin Patch']
groups = {}
for row in state['verified_ci_rows']:
    metric = row['metric']
    filename = '.github/profile-costs-baseline.txt' if metric.startswith('profile/') else '.github/perf-baseline-selfhost.txt' if metric.startswith('selfhost/') else '.github/perf-baseline.txt'
    groups.setdefault(filename, []).append(row)
for filename, rows in groups.items():
    before = (repo / filename).read_text()
    after = before
    patch.append('*** Update File: ' + str(repo / filename))
    for i, row in enumerate(rows):
        found = re.findall(r'^' + re.escape(row['metric']) + r'[\t ]+\d+[^\n]*$', before, re.M)
        assert len(found) == 1, row
        line = found[0]
        fields = line.split()
        assert int(fields[1]) == row['old'], (filename, row, line)
        updated = re.sub(r'(?<=\s)' + str(row['old']) + r'(?=\s|$)', str(row['measured']), line, count=1)
        assert updated != line
        note = ('# 2026-11-10: PR #15022, CI 42754163314 records the inherited HTTP benchmark changes.\n'
                '# Main da3282d0 and candidate 597b6e78 retain identical output in each local pair.\n'
                '# Evidence: docs/benchmarks/compiler-interpreter-http-ci-2026-11-10/. Tolerances stay unchanged.\n') if i == 0 else ''
        patch.extend(['@@', '-' + line] + ['+' + x for x in (note + updated).splitlines()])
        after = after.replace(line + '\n', note + updated + '\n', 1)
    copy(repo / filename, 'baselines/' + Path(filename).name + '.before.txt')
    (archive / 'baselines' / (Path(filename).name + '.after.txt')).write_text(after)
patch.append('*** End Patch')
(control / 'baseline-update.patch').write_text('\n'.join(patch) + '\n')
print(json.dumps({'archive': str(archive), 'banked_rows': len(state['verified_ci_rows']), 'files_before_manifest': len(list(archive.rglob('*')))}))
