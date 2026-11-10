from pathlib import Path
import json
import re
import shutil

control = Path(__file__).resolve().parent
original = control / 'reproduce-http.py'
prior = Path('/workspace/lang-interpreter-dispatch-20261109/build/interpreter-http-baselines-20261110-0520')
outpath = prior.parent / 'interpreter-http-baselines-20261110-0528'
text = original.read_text().split("for target in ['x86-64-linux', 'arm64-linux']:")[0]
text = text.replace("'build/interpreter-http-baselines-20261110-0520'", "'build/interpreter-http-baselines-20261110-0528'")
text = text.replace("if target == 'x86-64-linux' and native:", "if target == 'x86-64-linux' and native and profile == 'minimal':")
exec(compile(text, str(original), 'exec'), globals())
initial = json.loads((prior / 'result.json').read_text())
assert len(initial['pairs']) == 1 and len(initial['runs']) == 14
assert all(row.get('finished') for row in initial['runs'])
for file in prior.iterdir():
    if file.is_file() and file.name != 'result.json':
        shutil.copy2(file, out / file.name)
state = initial
state['resumed_from'] = {'path': str(prior), 'result_sha256': sha(prior / 'result.json')}
state['control_correction'] = (
    'The initial controller asserted equal instruction counts for byte-identical hardened images. '
    'Their counts differ. All original values and the failed assertion remain intact. '
    'No hardened instruction row requires a baseline update. The remaining dynamic control covers minimal only. '
    'This changes no product benchmark, comparator, tolerance or CI gate.'
)
files = {}
irs = []
for name in bins:
    asm = out / ('x86-64-linux-hardened-' + name + '.s')
    binary = out / ('x86-64-linux-hardened-' + name + '.bin')
    count = int(re.findall(r'^summary:\s*(\d+)\s*$', (out / ('x86-64-linux-hardened-' + name + '.callgrind')).read_text(), re.M)[0])
    files[name] = {'assembly_sha256': sha(asm), 'emit': sum(bool(re.match(rb'^\s', line)) for line in asm.read_bytes().splitlines()),
                   'assembly': asm.name, 'native_sha256': sha(binary), 'bytes': binary.stat().st_size,
                   'native': binary.name, 'guest_instructions': count}
    irs.append(count)
assert files['base']['assembly_sha256'] == files['candidate']['assembly_sha256']
assert files['base']['native_sha256'] == files['candidate']['native_sha256']
state['pairs'].append({'target': 'x86-64-linux', 'profile': 'hardened', 'files': files,
                       'guest_instructions': irs, 'instruction_counts_equal': irs[0] == irs[1]})
for suffix in ['emit', 'bytes']:
    state['metrics']['profile/x86-64-linux/http_hello.hardened.' + suffix] = files['base'][suffix]
save()
for profile in ['diagnostic', 'minimal']:
    compile_pair('x86-64-linux', profile)
for profile in ['safe', 'hardened', 'diagnostic', 'minimal']:
    compile_pair('arm64-linux', profile)
compile_pair('wasm32-wasi', native=False)
warnings = []
for filename in ['perf-x86-129627638421.log', 'perf-arm-129627638638.log']:
    for metric, measured, baseline in re.findall(r'##\[warning\]([^ ]+) measured (\d+) against a baseline of (\d+)', (control / filename).read_text()):
        value = state['metrics'][metric]
        same = value == int(measured)
        if not same:
            assert metric.endswith('.diagnostic.bytes'), (metric, value, measured)
        row = {'metric': metric, 'old': int(baseline), 'measured': int(measured),
               'local': value, 'local_matches_ci': same}
        if row not in warnings:
            warnings.append(row)
assert len(warnings) == 24
state.update(verified_ci_rows=warnings, finished=now(),
             limitations='All target/profile pairs retain byte-identical output across main and candidate. '
                         'CI supplies the banked values. Any local diagnostic-image size difference remains explicit. '
                         'ARM64 runtime counts come only from native CI, never from QEMU instruction counts.')
save()
print(json.dumps({'finished': state['finished'], 'ci_rows': len(warnings),
                  'local_ci_differences': [r for r in warnings if not r['local_matches_ci']],
                  'pairs': len(state['pairs']), 'commands_including_reused': len(state['runs'])}))
