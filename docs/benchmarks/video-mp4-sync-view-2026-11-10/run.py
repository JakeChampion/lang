#!/usr/bin/env python3
"""Measure explicit MP4 sync tables with one compiler and matched stdlib trees."""
import argparse
import hashlib
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
CONTAINERS = (b'moov', b'trak', b'mdia', b'minf', b'stbl')


def box(tag, body):
    return struct.pack('>I4s', len(body) + 8, tag) + body


def boxes(data):
    at = 0
    while at < len(data):
        size, tag = struct.unpack_from('>I4s', data, at)
        assert 8 <= size <= len(data) - at
        yield tag, data[at + 8:at + size]
        at += size
    assert at == len(data)


def add_sync(data, samples):
    output = bytearray()
    for tag, body in boxes(data):
        if tag in CONTAINERS:
            body = add_sync(body, samples)
        if tag == b'stbl' and samples is not None:
            body += box(b'stss', struct.pack('>II', 0, len(samples)) +
                        b''.join(struct.pack('>I', value) for value in samples))
        output += box(tag, body)
    return bytes(output)


def fixture(template, count, composition, pattern):
    # Reuse the prior independent metadata generator. Its absent-table file
    # keeps media before metadata, so added table bytes do not move packets.
    import importlib.util
    path = ROOT / 'docs/benchmarks/video-mp4-default-tables-2026-11-10/run.py'
    spec = importlib.util.spec_from_file_location('mp4_default_control', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    data, checksum = module.fixture(template, count, composition, False)
    samples = {'absent': None, 'empty': [], 'sparse': [1, count // 2, count],
               'dense': range(1, count + 1)}[pattern]
    data = add_sync(data, samples)
    return data, checksum - count + (count if samples is None else len(samples))


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
    vector_source = ROOT / 'tests/stdlib/video_mp4_vectors.fern'
    template = bytes.fromhex(re.search(r'Vector \{ name: "avc-bframes", bytes: "([0-9a-f]+)"', vector_source.read_text())[1])
    trees = {'before': config.before_stdlib.resolve(), 'after': config.after_stdlib.resolve()}
    report = {'platform': platform.platform(), 'compiler_sha256': hashlib.sha256(compiler.read_bytes()).hexdigest(),
              'source_sha256': hashlib.sha256(source.read_bytes()).hexdigest(),
              'template_sha256': hashlib.sha256(template).hexdigest(),
              'modules_sha256': {key: {name: hashlib.sha256((tree / name).read_bytes()).hexdigest()
                                      for name in ('std/video/mp4.fern.md', 'std/video/mp4/timing.fern.md')}
                                 for key, tree in trees.items()}, 'workloads': []}
    instructions = {'compiler_sha256': report['compiler_sha256'], 'source_sha256': report['source_sha256'],
                    'count': 262144, 'cases': []}
    with tempfile.TemporaryDirectory(prefix='fern-mp4-sync-') as temporary:
        temporary = Path(temporary)
        sampler = temporary / 'sample'
        subprocess.run(['cc', '-O2', '-Wall', '-Wextra', '-Werror', '-o', str(sampler),
                        str(ROOT / 'scripts/video-compare/sample.c')], check=True)
        binaries = {key: temporary / key for key in trees}
        for key, tree in trees.items():
            subprocess.run([str(compiler), '-stdlib', str(tree), '-target', 'x86-64-linux', '-o',
                            str(binaries[key]), str(source)], check=True, cwd=ROOT)
        for count in (65536, 262144):
            for composition, sync in ((c, s) for c in (False, True) for s in ('absent', 'empty', 'sparse', 'dense')):
                data, checksum = fixture(template, count, composition, sync)
                input_file = temporary / 'input.mp4'
                input_file.write_bytes(data)
                entry = {'packets': count, 'composition_table': composition, 'sync_pattern': sync,
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
                print(count, composition, sync, entry['summary'], flush=True)
                report['workloads'].append(entry)
                config.output.write_text(json.dumps(report, indent=2) + '\n')
                if config.instruction_output and count == 262144:
                    case = {'composition_table': composition, 'sync_pattern': sync,
                            'input_sha256': entry['input_sha256']}
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
