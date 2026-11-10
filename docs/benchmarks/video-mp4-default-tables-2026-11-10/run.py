#!/usr/bin/env python3
"""Measure MP4 index defaults with one compiler and matched stdlib trees."""
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


def rewrite(data, count, composition, sync, offset):
    output = bytearray()
    for tag, body in boxes(data):
        if tag in CONTAINERS:
            body = rewrite(body, count, composition, sync, offset)
        elif tag == b'edts':
            continue
        elif tag == b'stsz':
            body = struct.pack('>III', 0, 1, count)
        elif tag == b'stco':
            body = struct.pack('>III', 0, 1, offset)
        elif tag == b'stsc':
            body = struct.pack('>IIIII', 0, 1, 1, count, 1)
        elif tag == b'stts':
            body = struct.pack('>IIII', 0, 1, count, 1)
        elif tag == b'ctts':
            if not composition:
                continue
            body = struct.pack('>IIII', 0, 1, count, 2)
        elif tag == b'stss':
            if not sync:
                continue
            body = struct.pack('>III', 0, 1, 1)
        elif tag in (b'mdhd', b'mvhd'):
            assert body[0] == 0
            body = body[:12] + struct.pack('>II', 1, count) + body[20:]
        elif tag == b'tkhd':
            assert body[0] == 0
            body = body[:20] + struct.pack('>I', count) + body[24:]
        output += box(tag, body)
    return bytes(output)


def fixture(template, count, composition, sync):
    top = dict(boxes(template))
    assert all(tag in top for tag in (b'ftyp', b'mdat', b'moov'))
    prefix = box(b'ftyp', top[b'ftyp'])
    offset = len(prefix) + 8
    data = prefix + box(b'mdat', bytes(count)) + box(b'moov', rewrite(top[b'moov'], count, composition, sync, offset))
    checksum = 3 * count * (count - 1) // 2 + count * (offset + 2 + (2 if composition else 0)) + (1 if sync else count)
    return data, checksum


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
    with tempfile.TemporaryDirectory(prefix='fern-mp4-defaults-') as temporary:
        temporary = Path(temporary)
        sampler = temporary / 'sample'
        subprocess.run(['cc', '-O2', '-Wall', '-Wextra', '-Werror', '-o', str(sampler),
                        str(ROOT / 'scripts/video-compare/sample.c')], check=True)
        binaries = {key: temporary / key for key in trees}
        for key, tree in trees.items():
            subprocess.run([str(compiler), '-stdlib', str(tree), '-target', 'x86-64-linux', '-o',
                            str(binaries[key]), str(source)], check=True, cwd=ROOT)
        for count in (65536, 262144):
            for composition, sync in ((False, False), (False, True), (True, False), (True, True)):
                data, checksum = fixture(template, count, composition, sync)
                input_file = temporary / 'input.mp4'
                input_file.write_bytes(data)
                entry = {'packets': count, 'composition_table': composition, 'sync_table': sync,
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
                    case = {'composition_table': composition, 'sync_table': sync,
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
