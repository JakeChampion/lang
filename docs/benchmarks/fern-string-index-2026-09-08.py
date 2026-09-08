#!/usr/bin/env -S uv run --script
"""Measure general Fern workloads and compiler costs for checked index inlining.

Run from the repository in the disposable native ARM64 Linux /bench environment
described in ../COREUTILS-STRING-INDEX-2026-09-08.md. Output binaries and JSON
are generated under /bench/string-index-general, overwriting previous outputs.
Baseline compiler: /bench/fern-ssa-fix; candidate: /bench/fern-ssa-index.
"""

import hashlib
import json
import os
from pathlib import Path
import platform
import statistics
import subprocess
import time

if platform.system() != 'Linux' or platform.machine() != 'aarch64':
    raise RuntimeError('native ARM64 Linux is required')
root = Path('/bench/string-index-general')
root.mkdir(exist_ok=True)
compilers = {'before': '/bench/fern-ssa-fix', 'after': '/bench/fern-ssa-index'}
env = dict(os.environ, LC_ALL='C', LANG='C', TZ='UTC')
sources = [f'examples/bench/{name}.fern' for name in
           ['string_scan', 'sort_strings', 'int_loop', 'call_overhead', 'array_index', 'pmap_insert',
            'ascii_scan', 'tokenize']]
sources.append('coreutils/sort.fern')
start = time.perf_counter()
results = []
for source in sources:
    name = Path(source).stem
    outputs = {key: root / f'{name}-{key}' for key in compilers}
    instruction_counts = {}
    for key, compiler in compilers.items():
        assembly = subprocess.check_output([compiler, '-O', '-target', 'arm64-linux',
                                            '-backend', 'ssa', source], env=env, text=True, timeout=60)
        instruction_counts[key] = sum(line.startswith('\t') for line in assembly.splitlines())
    compile_samples = {key: [] for key in compilers}
    runtime_samples = {key: [] for key in compilers}
    for iteration in range(10):
        order = ['before', 'after'] if iteration % 2 == 0 else ['after', 'before']
        for key in order:
            before = time.perf_counter()
            subprocess.run([compilers[key], '-O', '-target', 'arm64-linux', '-backend', 'ssa',
                            '-o', str(outputs[key]), source], env=env, capture_output=True,
                           check=True, timeout=60)
            elapsed = time.perf_counter() - before
            if iteration >= 3:
                compile_samples[key].append(elapsed)
    verdicts = {}
    if not source.startswith('coreutils/'):
        # Benchmark examples intentionally encode their result as a nonzero exit.
        for key, binary in outputs.items():
            result = subprocess.run([str(binary)], env=env, capture_output=True, timeout=60)
            if result.returncode < 0:
                raise RuntimeError(f'{binary} crashed with {result.returncode}')
            verdicts[key] = (result.returncode, result.stdout.hex(), result.stderr.hex())
        if verdicts['before'] != verdicts['after']:
            raise RuntimeError(f'{source}: output/exit mismatch: {verdicts}')
        oracle = root / f'{name}-default'
        subprocess.run([compilers['before'], '-O', '-target', 'arm64-linux', '-o', str(oracle), source],
                       env=env, capture_output=True, check=True, timeout=60)
        reference = subprocess.run([str(oracle)], env=env, capture_output=True, timeout=60)
        verdicts['default'] = (reference.returncode, reference.stdout.hex(), reference.stderr.hex())
        if verdicts['default'] != verdicts['after']:
            raise RuntimeError(f'{source}: default backend disagrees: {verdicts}')
        for iteration in range(10):
            order = ['before', 'after'] if iteration % 2 == 0 else ['after', 'before']
            for key in order:
                before = time.perf_counter()
                result = subprocess.run([str(outputs[key])], env=env, capture_output=True, timeout=60)
                elapsed = time.perf_counter() - before
                if (result.returncode, result.stdout.hex(), result.stderr.hex()) != verdicts[key]:
                    raise RuntimeError(f'{outputs[key]} changed output during timing')
                if iteration >= 3:
                    runtime_samples[key].append(elapsed)
    entry = {'source': source, 'source_sha256': hashlib.sha256(Path(source).read_bytes()).hexdigest(),
             'compile_samples_seconds': compile_samples, 'runtime_samples_seconds': runtime_samples,
             'verdicts': verdicts, 'static_instructions': instruction_counts,
             'binary_bytes': {k: v.stat().st_size for k, v in outputs.items()},
             'binary_sha256': {k: hashlib.sha256(v.read_bytes()).hexdigest() for k, v in outputs.items()}}
    results.append(entry)
    print(source, 'bytes', entry['binary_bytes'], 'compile_ms',
          {k: round(statistics.mean(v) * 1000, 3) for k, v in compile_samples.items()},
          'runtime_ms', {k: round(statistics.mean(v) * 1000, 3) for k, v in runtime_samples.items() if v},
          flush=True)
report = {'platform': platform.platform(), 'elapsed_seconds': time.perf_counter() - start,
          'compiler_sha256': {k: hashlib.sha256(Path(v).read_bytes()).hexdigest() for k, v in compilers.items()},
          'results': results}
(root / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
print('Elapsed:', report['elapsed_seconds'], flush=True)
