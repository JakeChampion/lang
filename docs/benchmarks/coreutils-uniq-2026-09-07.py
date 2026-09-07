#!/usr/bin/env -S uv run --script
"""Uniq range-comparison experiment for #8278 and #8791; run only in a disposable Linux /bench.

Place native -O binaries at /bench/uniq-before and /bench/uniq and the uutils
multicall binary at /bench/uutils/coreutils. GNU 9.4+ must be in /usr/bin.
Run `uv run --script THIS_FILE 2000 pilot`, then change only 2000 to 200000.
Inputs and raw JSON are generated under /bench; existing names are overwritten.
Keep this experimental script with the measured report when publishing.
"""

import hashlib
import json
import os
from pathlib import Path
import platform
import statistics
import subprocess
import sys
import time

root = Path('/bench')
lines = int(sys.argv[1])
label = sys.argv[2]
if lines <= 0:
    raise ValueError('line count must be positive')
if platform.system() != 'Linux':
    raise RuntimeError('this experiment requires native Linux execution')
expected_machine = {'aarch64': 183, 'x86_64': 62}.get(platform.machine())
for binary in [root / 'uniq-before', root / 'uniq', Path('/usr/bin/uniq'), root / 'uutils/coreutils']:
    with binary.open('rb') as executable:
        header = executable.read(20)
    if header[:6] != bytes.fromhex('7f454c460201') or int.from_bytes(header[18:20], 'little') != expected_machine:
        raise RuntimeError(f'{binary} must be a native 64-bit little-endian ELF binary')
version_line = subprocess.check_output(['/usr/bin/uniq', '--version'], text=True).splitlines()[0]
version = tuple(int(v) for v in version_line.split()[-1].split('.'))
if '(GNU coreutils)' not in version_line or version < (9, 4):
    raise RuntimeError('GNU coreutils 9.4 or newer is required')
env = dict(os.environ, LC_ALL='C', LANG='C', TZ='UTC')
uu = str(root / 'uutils/coreutils')
impls = {'fern-before': [], 'fern': [], 'gnu': [], 'uutils': [uu]}
start = time.perf_counter()
duplicates = root / f'uniq-duplicates-{lines}'
distinct = root / f'uniq-distinct-{lines}'
paths = root / f'uniq-paths-{lines}'
with duplicates.open('w') as dup, distinct.open('w') as unique, paths.open('w') as path:
    for i in range(lines):
        dup.write(f'line {i // 4}\n')
        unique.write(f'{i + 1}\n')
        path.write(f'/usr/lib/x86_64-linux-gnu/libfoo-{i:07d}.so\n')
cases = [('uniq', args, duplicates) for args in [[], ['-c'], ['-d'], ['-f1', '-c']]]
cases += [('uniq', args, distinct) for args in [[], ['-u']]]
cases += [('uniq', [], paths)]
results = []
for utility, args, source in cases:
    commands = {name: prefix + [str(root / (utility + '-before')) if name == 'fern-before' else str(root / utility) if name == 'fern' else '/usr/bin/' + utility if name == 'gnu' else utility] + args + [str(source)] for name, prefix in impls.items()}
    oracle = None
    samples = {name: [] for name in impls}
    for name, cmd in commands.items():
        check = subprocess.run(cmd, env=env, capture_output=True, timeout=60)
        verdict = (check.returncode, hashlib.sha256(check.stdout).hexdigest(), check.stderr)
        if oracle is None:
            oracle = verdict
        elif verdict != oracle:
            raise RuntimeError(f'{utility} {args}: parity failed for {name}: {verdict} != {oracle}')
        if check.returncode:
            raise RuntimeError(f'{utility} {args} failed: {check.stderr!r}')
    with open(os.devnull, 'wb') as sink:
        # Rotate order across rounds to avoid measuring all Fern samples cold.
        names = list(impls)
        for iteration in range(10):
            for name in names[iteration % len(names):] + names[:iteration % len(names)]:
                before = time.perf_counter()
                subprocess.run(commands[name], env=env, stdout=sink, stderr=subprocess.PIPE, check=True, timeout=60)
                elapsed = time.perf_counter() - before
                if iteration >= 3:
                    samples[name].append(elapsed)
    result = {'utility': utility, 'args': args, 'input_bytes': source.stat().st_size, 'input_sha256': hashlib.sha256(source.read_bytes()).hexdigest(), 'samples_seconds': samples}
    results.append(result)
    print(utility, args, {name: round(statistics.mean(values) * 1000, 3) for name, values in samples.items()}, flush=True)
report = {'label': label, 'lines': lines, 'platform': platform.platform(), 'gnu': subprocess.check_output(['/usr/bin/cat', '--version'], text=True).splitlines()[0], 'uutils': subprocess.check_output([uu, 'cat', '--version'], text=True).strip(), 'binary_sha256': {u: hashlib.sha256((root / u).read_bytes()).hexdigest() for u in ['uniq-before', 'uniq']}, 'elapsed_seconds': time.perf_counter() - start, 'results': results}
(root / f'{label}-{lines}.json').write_text(json.dumps(report, indent=2) + '\n')
print('Elapsed:', report['elapsed_seconds'], flush=True)
