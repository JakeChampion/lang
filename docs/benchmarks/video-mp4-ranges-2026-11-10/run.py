#!/usr/bin/env python3
"""Compare MP4 media searches with one compiler and matched source trees."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import statistics
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[3]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--compiler', type=Path, required=True)
parser.add_argument('--before-stdlib', type=Path, required=True)
parser.add_argument('--after-stdlib', type=Path, required=True)
parser.add_argument('--output', type=Path, required=True)
config = parser.parse_args()
compiler = config.compiler.resolve()
source = ROOT / 'scripts/video-reference/mp4-ranges.fern'
trees = {'before': config.before_stdlib.resolve(), 'after': config.after_stdlib.resolve()}
workloads = [(1, 4096, 10, 'last'), (8, 4096, 10, 'scattered'),
             (16, 4096, 10, 'scattered'), (32, 4096, 10, 'scattered'), (128, 4096, 10, 'scattered'),
             (2048, 4096, 10, 'last'), (2048, 4096, 10, 'scattered')]
report = {'platform': platform.platform(), 'compiler_sha256': hashlib.sha256(compiler.read_bytes()).hexdigest(),
          'source_sha256': hashlib.sha256(source.read_bytes()).hexdigest(),
          'tables_sha256': {key: hashlib.sha256((tree / 'std/audio/mp4/tables.fern.md').read_bytes()).hexdigest()
                            for key, tree in trees.items()}, 'workloads': []}
with tempfile.TemporaryDirectory(prefix='fern-mp4-ranges-') as temporary:
    temporary = Path(temporary)
    sampler = temporary / 'sample'
    subprocess.run(['cc', '-O2', '-Wall', '-Wextra', '-Werror', '-o', str(sampler),
                    str(ROOT / 'scripts/video-compare/sample.c')], check=True)
    binaries = {key: temporary / key for key in trees}
    for key, tree in trees.items():
        subprocess.run([str(compiler), '-stdlib', str(tree), '-target', 'x86-64-linux', '-o',
                        str(binaries[key]), str(source)], check=True, cwd=ROOT)
    for ranges, count, rounds, mode in workloads:
        argv = list(map(str, (ranges, count, rounds, mode)))
        checksum = rounds * sum(8589934592 + (i * 73 % ranges if mode == 'scattered' else ranges - 1) * 16 + i * 7
                                for i in range(count))
        entry = {'ranges': ranges, 'packets': count, 'rounds': rounds, 'mode': mode,
                 'expected_checksum': checksum, 'trials': {'before': [], 'after': []}, 'summary': {}}
        for group in range(7):
            order = ['before', 'after'] if group % 2 == 0 else ['after', 'before']
            for key in order:
                rss = temporary / 'rss.txt'
                start = time.perf_counter()
                output = subprocess.check_output([str(sampler), str(rss), str(binaries[key]), *argv], text=True)
                elapsed = time.perf_counter() - start
                actual, allocations = map(int, output.split())
                assert actual == checksum, (key, actual, checksum)
                entry['trials'][key].append({'elapsed_seconds': elapsed, 'peak_rss_kib': json.loads(rss.read_text())['peak_rss_bytes'] // 1024,
                                             'allocations': allocations})
        for key in trees:
            trace = temporary / f'{key}.cachegrind'
            output = subprocess.check_output(['valgrind', '--tool=cachegrind', '--cache-sim=no', '--branch-sim=no',
                                              '--log-file=' + str(temporary / 'valgrind.log'),
                                              '--cachegrind-out-file=' + str(trace), str(binaries[key]), *argv], text=True)
            assert int(output.split()[0]) == checksum
            summary = next(line for line in trace.read_text().splitlines() if line.startswith('summary:'))
            rows = entry['trials'][key]
            assert len({row['allocations'] for row in rows}) == 1
            entry['summary'][key] = {'ir': int(summary.split()[1]), 'allocations': rows[0]['allocations'],
                                    'median_elapsed_seconds': statistics.median(row['elapsed_seconds'] for row in rows),
                                    'median_peak_rss_kib': statistics.median(row['peak_rss_kib'] for row in rows)}
        report['workloads'].append(entry)
        print(entry['ranges'], entry['mode'], entry['summary'], flush=True)
        config.output.write_text(json.dumps(report, indent=2) + '\n')
