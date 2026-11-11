#!/usr/bin/env python3
"""Compare MP4 composition runs with one compiler and matched library trees."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import platform
import re
import statistics
import struct
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[3]
GENERATOR = ROOT / 'docs/benchmarks/video-mp4-default-tables-2026-11-10/run.py'
spec = importlib.util.spec_from_file_location('mp4_defaults', GENERATOR)
defaults = importlib.util.module_from_spec(spec)
spec.loader.exec_module(defaults)


def add_table(data, table):
    output = bytearray()
    for tag, body in defaults.boxes(data):
        if tag in defaults.CONTAINERS:
            body = add_table(body, table)
        if tag == b'stbl':
            body += table
        output += defaults.box(tag, body)
    return bytes(output)


def fixture(template, count, mode):
    data, checksum = defaults.fixture(template, count, False, True)
    if mode == 'absent':
        return data, checksum, 0
    width = {'constant': count, 'clustered': 11,
             'signed-single': 1, 'unsigned-single': 1}[mode]
    version = int(mode in ('clustered', 'signed-single'))
    values = {'constant': (2,), 'clustered': (-2147483648, 0, 17, 2147483647),
              'signed-single': (-1, 2), 'unsigned-single': (0, 4294967295)}[mode]
    runs = []
    at = 0
    while at < count:
        length = min(width, count - at)
        value = values[len(runs) % len(values)]
        runs.append((length, value))
        checksum += length * value
        at += length
    table = defaults.box(b'ctts', struct.pack('>II', version << 24, len(runs)) +
                         b''.join(struct.pack('>II', n, v & 0xffffffff) for n, v in runs))
    return add_table(data, table), checksum, len(runs)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--compiler', required=True, type=Path)
    parser.add_argument('--before-stdlib', required=True, type=Path)
    parser.add_argument('--after-stdlib', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--instruction-output', type=Path)
    config = parser.parse_args()
    compiler = config.compiler.resolve()
    source = ROOT / 'scripts/video-reference/mp4-defaults.fern'
    sampler_source = ROOT / 'scripts/video-compare/sample.c'
    vectors = ROOT / 'tests/stdlib/video_mp4_vectors.fern'
    template = bytes.fromhex(re.search(r'Vector \{ name: "avc-bframes", bytes: "([0-9a-f]+)"', vectors.read_text())[1])
    trees = {'before': config.before_stdlib.resolve(), 'after': config.after_stdlib.resolve()}
    report = {'platform': platform.platform(), 'compiler_sha256': digest(compiler),
              'source_sha256': digest(source), 'sampler_sha256': digest(sampler_source),
              'generator_sha256': digest(GENERATOR), 'run_sha256': digest(Path(__file__)),
              'template_sha256': hashlib.sha256(template).hexdigest(),
              'modules_sha256': {key: {name: digest(tree / name) for name in
                                      ('std/video/mp4.fern.md', 'std/video/mp4/timing.fern.md')}
                                 for key, tree in trees.items()}, 'workloads': []}
    instructions = {key: report[key] for key in ('compiler_sha256', 'source_sha256', 'modules_sha256')}
    instructions.update({'count': 262144, 'cases': []})
    with tempfile.TemporaryDirectory(prefix='fern-mp4-composition-') as directory:
        temporary = Path(directory)
        sampler = temporary / 'sample'
        subprocess.run(['cc', '-O2', '-Wall', '-Wextra', '-Werror', '-o', str(sampler),
                        str(sampler_source)], check=True)
        binaries = {key: temporary / key for key in trees}
        for key, tree in trees.items():
            subprocess.run([str(compiler), '-stdlib', str(tree), '-target', 'x86-64-linux', '-o',
                            str(binaries[key]), str(source)], check=True, cwd=ROOT)
        for count in (65536, 262144):
            for mode in ('absent', 'constant', 'clustered', 'signed-single', 'unsigned-single'):
                data, checksum, runs = fixture(template, count, mode)
                input_file = temporary / 'input.mp4'
                input_file.write_bytes(data)
                entry = {'packets': count, 'composition': mode, 'runs': runs,
                         'input_sha256': hashlib.sha256(data).hexdigest(), 'input_bytes': len(data),
                         'expected_checksum': checksum, 'trials': {'before': [], 'after': []}, 'summary': {}}
                for group in range(7):
                    order = ['before', 'after'] if group % 2 == 0 else ['after', 'before']
                    for key in order:
                        usage = temporary / 'usage.json'
                        start = time.perf_counter()
                        output = subprocess.check_output([str(sampler), str(usage), str(binaries[key]), str(input_file)], text=True)
                        elapsed = time.perf_counter() - start
                        actual_count, actual_checksum, allocations = map(int, output.split())
                        assert (actual_count, actual_checksum) == (count, checksum)
                        row = json.loads(usage.read_text())
                        row.update({'elapsed_seconds': elapsed, 'allocations': allocations})
                        entry['trials'][key].append(row)
                for key, rows in entry['trials'].items():
                    assert len({row['allocations'] for row in rows}) == 1
                    entry['summary'][key] = {'allocations': rows[0]['allocations'],
                                             'median_peak_rss_bytes': statistics.median(row['peak_rss_bytes'] for row in rows),
                                             'median_elapsed_seconds': statistics.median(row['elapsed_seconds'] for row in rows)}
                print(count, mode, entry['summary'], flush=True)
                report['workloads'].append(entry)
                config.output.write_text(json.dumps(report, indent=2) + '\n')
                if config.instruction_output and count == 262144:
                    case = {'composition': mode, 'input_sha256': entry['input_sha256']}
                    for key in trees:
                        trace = temporary / f'{key}.cachegrind'
                        output = subprocess.check_output(['valgrind', '--tool=cachegrind', '--cache-sim=no', '--branch-sim=no',
                                                          '--log-file=' + str(temporary / 'valgrind.log'),
                                                          '--cachegrind-out-file=' + str(trace), str(binaries[key]),
                                                          str(input_file)], text=True)
                        actual_count, actual_checksum, allocations = map(int, output.split())
                        assert (actual_count, actual_checksum) == (count, checksum)
                        summary = next(line for line in trace.read_text().splitlines() if line.startswith('summary:'))
                        case[key] = {'instructions': int(summary.split()[1]), 'allocations': allocations}
                    instructions['cases'].append(case)
                    print('instructions', case, flush=True)
                    config.instruction_output.write_text(json.dumps(instructions, indent=2) + '\n')


if __name__ == '__main__':
    main()
