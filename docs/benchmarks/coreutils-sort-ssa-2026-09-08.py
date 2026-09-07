#!/usr/bin/env -S uv run --script
"""Default versus corrected SSA sort experiment for #8278; run only in a disposable Linux /bench.

Place native -O binaries at /bench/sort and /bench/sort-ssa-fixed and the uutils
multicall binary at /bench/uutils/coreutils. GNU 9.4+ must be in /usr/bin.
Run `uv run --script THIS_FILE 2000 pilot`, then change only 2000 to 200000.
Inputs and raw JSON are generated under /bench; existing names are overwritten.
See ../COREUTILS-SSA-ARGS-2026-09-08.md for build and measurement details.
"""

import hashlib
import json
import os
from pathlib import Path
import platform
import random
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
for binary in [root / 'sort', root / 'sort-ssa-fixed', Path('/usr/bin/sort'), root / 'uutils/coreutils']:
    with binary.open('rb') as executable:
        header = executable.read(20)
    if header[:6] != bytes.fromhex('7f454c460201') or int.from_bytes(header[18:20], 'little') != expected_machine:
        raise RuntimeError(f'{binary} must be a native 64-bit little-endian ELF binary')
version_line = subprocess.check_output(['/usr/bin/sort', '--version'], text=True).splitlines()[0]
version = tuple(int(v) for v in version_line.split()[-1].split('.'))
if '(GNU coreutils)' not in version_line or version < (9, 4):
    raise RuntimeError('GNU coreutils 9.4 or newer is required')
env = dict(os.environ, LC_ALL='C', LANG='C', TZ='UTC')
uu = str(root / 'uutils/coreutils')
impls = {'fern-default': [], 'fern-ssa': [], 'gnu': [], 'uutils': [uu]}
start = time.perf_counter()
rng = random.Random(8302)
words = root / f'words-{lines}'
numbers = root / f'numbers-{lines}'
shuffled_numbers = root / f'shuffled-numbers-{lines}'
words.write_bytes(b''.join(bytes(rng.randrange(97, 123) for _ in range(11)) + b'\n' for _ in range(lines)))
numbers.write_bytes(b''.join(str(i).encode() + b'\n' for i in range(1, lines + 1)))
shuffled_numbers.write_bytes(b''.join(str(rng.randrange(-10**9, 10**9)).encode() + b'\n' for _ in range(lines)))
sorted_words = root / f'sorted-{lines}'
prefix_words = root / f'prefix-words-{lines}'
prefix_words.write_bytes(b''.join(b'a' * 128 + word + b'\n' for word in words.read_bytes().splitlines()))
with sorted_words.open('wb') as out:
    subprocess.run(['/usr/bin/sort', str(words)], stdout=out, env=env, check=True)
cases = [('sort', args, words) for args in [[], ['-r']]]
cases += [('sort', ['-n'], shuffled_numbers)]
cases += [('sort', ['-c'], sorted_words)]
cases += [('sort', [], prefix_words)]
results = []
for utility, args, source in cases:
    commands = {name: prefix + [str(root / utility) if name == 'fern-default' else str(root / (utility + '-ssa-fixed')) if name == 'fern-ssa' else '/usr/bin/' + utility if name == 'gnu' else utility] + args + [str(source)] for name, prefix in impls.items()}
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
report = {'label': label, 'lines': lines, 'platform': platform.platform(), 'gnu': subprocess.check_output(['/usr/bin/cat', '--version'], text=True).splitlines()[0], 'uutils': subprocess.check_output([uu, 'cat', '--version'], text=True).strip(), 'binary_sha256': {u: hashlib.sha256((root / u).read_bytes()).hexdigest() for u in ['sort', 'sort-ssa-fixed']}, 'elapsed_seconds': time.perf_counter() - start, 'results': results}
(root / f'{label}-{lines}.json').write_text(json.dumps(report, indent=2) + '\n')
print('Elapsed:', report['elapsed_seconds'], flush=True)
