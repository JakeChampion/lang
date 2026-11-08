"""Check pure Fern bit fields with byte equations and pinned offline code."""

import argparse
import hashlib
from itertools import islice
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess

UPSTREAM = {
    'lib/jxl/dec_bit_reader.cc': '02aa1e4b4ecc5b742421b8a130facbafc9f8d439d4c99dd08aea44c1498269ce',
    'lib/jxl/base/status.h': '30101c43cc5ebf09a13587aca1a1739fa69d60e5f47dfa1766811d8b638b14e9',
    'lib/jxl/base/compiler_specific.h': 'a05ed469d414dbf4b423bb25c8c1fd84042f93a05565c71dc58177de673b9e09',
    'lib/jxl/base/sanitizer_definitions.h': '059ef67d7b549bf8655bc508424cc3afb597617f9c27f0c0f48ca290a1560517',
    'lib/jxl/base/common.h': '117912699c47463593dcc126efbcf5814cbb382c3376f39b25a361abc1b01822',
    'lib/jxl/dec_bit_reader.h': '6e9de90c126b0312b6b8dc582c2b629bba53ef7b401654583fd1c58d77a0755b',
    'lib/jxl/base/byte_order.h': '8245ac875798533d1faaf026520a87a2def0097da4f4f22608e2de943f83db81',
    'lib/include/jxl/types.h': '317bf87d55b133c073b7fa48436c8e2ab3a7c48a2263f26f4eaa407090f93f6d',
    'LICENSE': '8405932022a556380c2d8c272eff154a923feb197233f348ce5f7334fb0a5ede',
}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def pack(fields, budget):
    if budget < 0:
        return dict(error='Limit')
    integer, length = 0, 0
    for value, width in fields:
        if not 0 <= width <= 56:
            return dict(error='Width')
        if value >= 1 << width:
            return dict(error='Value')
        integer |= value << length
        length += width
        if length > budget * 8:
            return dict(error='Limit')
    return dict(bits=length, data=integer.to_bytes((length + 7) // 8, 'little'))


def read(data, position, width, extent):
    if not 0 <= width <= 56:
        return 'error Width'
    if not 0 <= position <= extent <= len(data) * 8:
        return 'error Bounds'
    if position + width > extent:
        return 'error Truncated'
    value = (int.from_bytes(data, 'little') >> position) % (1 << width)
    return f'ok {value} {position + width}'


def align(data, position, extent):
    admission = read(data, position, 0, extent)
    if admission.startswith('error'):
        return admission
    padding = read(data, position, (-position) % 8, extent)
    if padding.startswith('error'):
        return padding
    _, value, next_bit = padding.split()
    return 'error Padding' if int(value) else f'ok {next_bit}'


def fixture(case):
    fields, queries, data = case['fields'], case['queries'], case['data']
    return (struct.pack('<iIII', case['budget'], len(fields), len(queries), len(data))
            + b''.join(struct.pack('<iQ', width, value) for value, width in fields)
            + b''.join(struct.pack('<qiq', position, width, extent) for position, width, extent in queries) + data)


def cases():
    def make(name, fields, data=b'\xd5\x6a' * 8, queries=None, budget=2048):
        if queries is None:
            queries = [(0, 3, len(data) * 8), (3, 5, len(data) * 8), (7, 56, len(data) * 8)]
        return dict(name=name, fields=fields, data=data, queries=queries, budget=budget)
    for width in range(57):
        for offset in range(8):
            for value in [0, (1 << width) - 1, ((1 << width) - 1) // 3]:
                yield make(f'field-{width}-{offset}-{value}', [(0, offset), (value, width)])
    for width in range(57):
        yield make(f'value-overflow-{width}', [(1 << width, width)])
    for width in [-1, 57, 2147483647]:
        yield make(f'width-{width}', [(0, width)])
    for budget in range(-1, 129):
        yield make(f'budget-{budget}', [(1, 56)] * 18, budget=budget)
    for count in [0, 1, 2, 7, 31, 257, 1024]:
        yield make(f'zero-width-count-{count}', [(0, 0)] * count, budget=0)
    inputs = [b'', b'\0', b'\xff', bytes(16), b'\xff' * 16, bytes(range(16)),
              bytes((i * 37 + 11) % 256 for i in range(16)), bytes((i * 37) % 256 for i in range(65536))]
    for i, data in enumerate(inputs):
        positions = list(range(min(65, len(data) * 8 + 1))) + [len(data) * 8]
        queries = [(p, width, len(data) * 8) for p in positions for width in range(57)]
        yield make(f'input-{i}', [], data, queries, 0)
    data = bytes((i * 37 + 11) % 256 for i in range(16))
    for count in range(17):
        prefix = data[:count]
        queries = [(position, width, len(prefix) * 8) for position in range(8) for width in range(57)]
        yield make(f'byte-prefix-{count}', [], prefix, queries, 0)
    queries = [(p, w, e) for p in [-1, 0, 1, 7, 8, 16, 17, 9223372036854775807]
               for w in [-1, 0, 1, 7, 8, 56, 57, 2147483647]
               for e in [-1, 0, 1, 7, 8, 16, 17, 9223372036854775807]]
    yield make('malformed-query-domains', [], b'\xff\x80', queries, 0)
    for value in [0, 1, 85, 128, 255]:
        queries = [(p, 0, e) for e in range(17) for p in range(e + 1)]
        yield make(f'padding-{value}', [], bytes([value, value]), queries, 0)


def check_worker(stdout, stderr, selected):
    wanted = []
    for index, case in enumerate(selected):
        result = pack(case['fields'], case['budget'])
        if 'error' in result:
            wanted.append(f'{index} pack error {result["error"]}')
        else:
            wanted.append(f'{index} pack ok {result["bits"]} {result["data"].hex()}')
        for i, (position, width, extent) in enumerate(case['queries']):
            wanted.append(f'{index} query {i} ' + read(case['data'], position, width, extent))
            wanted.append(f'{index} align {i} ' + align(case['data'], position, extent))
    heaps = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
    if stdout.splitlines() != wanted or not heaps or not all(int(a) > 0 and a == b and c == '0' for a, b, c in heaps):
        raise ValueError('bit results or heap evidence differ')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fern', type=Path, required=True)
    parser.add_argument('--target', choices=['arm64-darwin', 'arm64-linux', 'x86-64-linux', 'wasm32-wasi'], required=True)
    parser.add_argument('--runner', action='append', default=[])
    parser.add_argument('--reference-source', type=Path, required=True)
    parser.add_argument('--cxx', default='c++')
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--max-cases', type=int)
    args = parser.parse_args()
    if args.max_cases is not None and args.max_cases <= 0:
        parser.error('max-cases must be positive')
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    repo = Path(__file__).resolve().parents[2]
    reference = args.reference_source.resolve()
    if {p: digest(reference / p) for p in UPSTREAM} != UPSTREAM:
        raise ValueError('offline reference source differs from libjxl 0.12.0')
    compiler = args.fern.resolve()
    cxx = Path(shutil.which(args.cxx) or args.cxx).resolve()
    sources = ['fern-image/packages/codecs/jxl/bits.fern', 'fern-image/tests/jxl_bits_corpus.fern',
               'fern-image/tests/reference_jxl_bits.cc', 'fern-image/tests/qualify_jxl_bits.py']
    frozen = {p: digest(repo / p) for p in sources}
    tools = dict(compiler=digest(compiler), cxx=digest(cxx))
    record = dict(complete=False, target=args.target, source_inventory=frozen, tool_sha256=tools,
                  upstream_sha256=UPSTREAM, commands=[], observations=[])
    env = dict(os.environ, FERN_LEAKCHECK='1')

    def run(label, command, data=None, cwd=out):
        r = subprocess.run(list(map(str, command)), input=data, cwd=cwd, env=env, capture_output=True, timeout=120)
        for stream in ['stdout', 'stderr']:
            (out / (label + '.' + stream)).write_bytes(getattr(r, stream))
        record['commands'].append(dict(label=label, command=list(map(str, command)), exit_code=r.returncode,
                                       stdout_sha256=digest(out / (label + '.stdout')), stderr_sha256=digest(out / (label + '.stderr'))))
        if r.returncode or label.endswith('build') or label.startswith('batch-'):
            (out / 'proof.json').write_text(json.dumps(record, indent=2) + '\n')
        r.check_returncode()
        return r.stdout.decode(), r.stderr.decode()

    oracle = out / 'reference'
    run('reference-build', [cxx, '-std=c++17', '-O2', '-DNDEBUG', '-Wall', '-Wextra', '-Werror',
                           '-isystem', reference, '-isystem', reference / 'lib/include',
                           repo / 'fern-image/tests/reference_jxl_bits.cc', reference / 'lib/jxl/dec_bit_reader.cc', '-o', oracle])
    binary = out / ('worker.wasm' if args.target == 'wasm32-wasi' else 'worker')
    run('worker-build', [compiler, '-target', args.target, '-sanitize', '-o', binary,
                         repo / 'fern-image/tests/jxl_bits_corpus.fern', repo / 'internal/stdlib'], cwd=repo)
    record['binary_sha256'] = dict(worker=digest(binary), reference=digest(oracle))
    selected = list(islice(cases(), args.max_cases))
    for start in range(0, len(selected), 16):
        batch = selected[start:start + 16]
        paths = []
        for i, case in enumerate(batch, start):
            path = out / f'input-{i}.bin'
            path.write_bytes(fixture(case))
            paths.append(path)
        stdout, stderr = run('batch-' + str(start), [*args.runner, binary, *(p.name for p in paths)])
        check_worker(stdout, stderr, batch)
        for i, (case, path) in enumerate(zip(batch, paths, strict=True), start):
            result = pack(case['fields'], case['budget'])
            row = dict(name=case['name'], input_sha256=digest(path), fields=len(case['fields']), queries=len(case['queries']))
            if 'data' in result:
                packed = path.with_name(path.name + '.packed')
                if packed.read_bytes() != result['data']:
                    raise ValueError('complete packed bytes differ')
                row['packed_sha256'] = digest(packed)
                offset, expected, commands = 0, [], []
                for value, width in case['fields']:
                    commands.append(f'{offset} {width}\n')
                    offset += width
                    expected.append(f'ok {value} {offset}')
                if commands:
                    actual, _ = run('packed-reference-' + str(i), [oracle, packed], ''.join(commands).encode())
                    if actual.splitlines() != expected:
                        raise ValueError('reference packed fields differ')
                row['reference_packed_fields'] = len(commands)
            data = out / f'raw-{i}.bin'
            data.write_bytes(case['data'])
            queries = [(p, w) for p, w, _ in case['queries'] if 0 <= p <= len(case['data']) * 8 and 0 <= w <= 56]
            commands = ''.join(f'{p} {w}\n' for p, w in queries).encode()
            actual, _ = run('raw-reference-' + str(i), [oracle, data], commands)
            expected = [read(case['data'], p, w, len(case['data']) * 8).replace('error Truncated', 'truncated') for p, w in queries]
            if actual.splitlines() != expected:
                raise ValueError('reference raw fields differ')
            row['reference_raw_fields'] = len(queries)
            record['observations'].append(row)
        (out / 'proof.json').write_text(json.dumps(record, indent=2) + '\n')
    if frozen != {p: digest(repo / p) for p in sources} or UPSTREAM != {p: digest(reference / p) for p in UPSTREAM}:
        raise ValueError('source changed during qualification')
    if tools != dict(compiler=digest(compiler), cxx=digest(cxx)):
        raise ValueError('tool changed during qualification')
    record.update(complete=True, cases=len(selected))
    (out / 'proof.json').write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps(dict(complete=True, cases=len(selected), target=args.target)))


if __name__ == '__main__':
    main()
