"""Check Fern headers with independent equations and pinned upstream classes."""

import argparse
import base64
import hashlib
from itertools import islice, product
import json
import os
from pathlib import Path
import platform
import re
import shutil
import struct
import subprocess

from qualify_jxl_bits import digest, pack, read
from qualify_jxl_integers import UPSTREAM as INTEGER_SOURCES, fields32, decode as integer_read

UPSTREAM = dict(INTEGER_SOURCES, **{
    'lib/jxl/headers.h': '92de8549944ee9cbb2e3697f8e58af96fed1c31df2165dafc00895f5e1888098',
    'lib/jxl/headers.cc': '106f6d0e646312b4cf5afb8e8d6051e0d8e849f29c3bdbd918c92aebda25c9fd',
    'lib/jxl/frame_dimensions.h': '6c8d841d1d15e059d31ed09f26a2edf1148aebb18c633194e4c16de4073b22a8',
    'lib/jxl/base/rect.h': 'd0810e77c4087e28a2f09dec8f9f222d79a41e8346c361ecfa278cbc7aa082a9',
})
RATIOS = [(1, 1), (12, 10), (4, 3), (3, 2), (16, 9), (5, 4), (2, 1)]
NUMERATOR = [(0, 100), (0, 1000), (10, 1), (30, 1)]
DENOMINATOR = [(0, 1), (0, 1001), (8, 1), (10, 1)]
LOOPS = [(0, 0), (3, 0), (16, 0), (32, 0)]


def choices(preview, divided):
    if not preview:
        return [(9, 1), (13, 1), (18, 1), (30, 1)]
    return [(0, 16), (0, 32), (5, 1), (9, 33)] if divided else [(6, 1), (8, 65), (10, 321), (12, 1345)]


def fields(mode, values, fallback=True):
    a, b, c, d = values
    if mode == 2:
        result = []
        for value, distribution in zip([a, b, c], [NUMERATOR, DENOMINATOR, LOOPS], strict=True):
            encoded = fields32(value, distribution)
            if 'error' in encoded:
                return dict(error='Value')
            result += encoded['fields']
        return dict(fields=result + [(int(bool(d)), 1)])
    if not 0 < a < 2**32 or not 0 < b < 2**32:
        return dict(error='Dimension')
    preview = mode == 1
    ratios = [i + 1 for i, (n, denominator) in enumerate(RATIOS) if a == b * n // denominator]
    ratio = ratios[0] if ratios else 0
    divided = a % 8 == b % 8 == 0 if preview else b <= 256 and b % 8 == 0 and (ratio != 0 or a <= 256 and a % 8 == 0)
    if preview and fallback and (b > 4352 or (ratio == 0 and a > 4352)):
        divided = False
    def scalar(value):
        if not preview and divided:
            return dict(fields=[(value // 8 - 1, 5)])
        return fields32(value // 8 if divided else value, choices(preview, divided))
    height = scalar(b)
    if 'error' in height:
        return dict(error='Value')
    result = [(int(divided), 1), *height['fields'], (ratio, 3)]
    if ratio == 0:
        width = scalar(a)
        if 'error' in width:
            return dict(error='Value')
        result += width['fields']
    return dict(fields=result)


def decode(data, position, extent, mode):
    def advance(result):
        nonlocal position
        if result.startswith('error'):
            error = result.removeprefix('error ')
            raise ValueError(error if error.startswith('Input(') else 'Input(' + error + ')')
        _, value, next_bit = result.split()
        position = int(next_bit)
        return int(value)
    def take(width):
        return advance(read(data, position, width, extent))
    def integer(distribution):
        return advance(integer_read(data, position, extent, 1, distribution))
    try:
        if mode == 2:
            n, d, loops, timecodes = integer(NUMERATOR), integer(DENOMINATOR), integer(LOOPS), take(1)
            return f'animation {n} {d} {loops} {timecodes} {position}'
        divided = take(1)
        def scalar():
            if mode == 0 and divided:
                return (take(5) + 1) * 8
            return integer(choices(mode == 1, divided)) * (8 if divided else 1)
        height = scalar()
        ratio = take(3)
        width = height * RATIOS[ratio - 1][0] // RATIOS[ratio - 1][1] if ratio else scalar()
        return f'dimensions {width} {height} {position}'
    except ValueError as error:
        return 'error ' + str(error)


def encoded(case):
    result = fields(case['mode'], case['values'])
    return result if 'error' in result else pack([(0, case['prefix']), *result['fields']], case['budget'])


def make(name, mode, values, prefix=0, raw_fields=None, budget=12):
    result = fields(mode, values)
    raw = raw_fields if raw_fields is not None else result.get('fields', [(0, 56), (0, 24)])
    packed = pack([(0, max(0, min(7, prefix))), *raw], 20)
    data, length = packed['data'], packed['bits']
    queries = [(max(0, min(7, prefix)), extent) for extent in range(length + 1)]
    queries += [(p, len(data) * 8) for p in range(min(8, len(data) * 8 + 1))]
    queries += [(-1, length), (length + 1, length), (0, -1), (0, len(data) * 8 + 1), (2**63 - 1, length)]
    return dict(name=name, mode=mode, values=values, prefix=prefix, data=data, queries=queries,
                budget=budget, canonical=raw_fields is None and 'fields' in result)


def cases():
    dimensions = [[1, 2, 7, 8, 9, 128, 256, 257, 512, 513, 8192, 8193, 262144, 262145, 2**30],
                  [1, 8, 32, 64, 65, 128, 256, 264, 320, 321, 1344, 1345, 4352, 4360, 5440]]
    for mode, heights in enumerate(dimensions):
        for height, prefix in product(heights, range(8)):
            for width in [1, 3, height, height * 2]:
                yield make(f'dimensions-{mode}-{width}-{height}-{prefix}', mode, (width, height, 0, 0), prefix)
    for x, y in product(range(1, 33), repeat=2):
        yield make(f'compact-{x}-{y}', 0, (x * 8, y * 8, 0, 0))
    for mode, height, (n, d) in product(range(2), range(1, 34), RATIOS):
        yield make(f'ratio-{mode}-{height}-{n}-{d}', mode, (height * n // d, height, 0, 0), 3)
    for mode, divided, selector, ratio, maximum in product(range(2), range(2), range(4), range(8), [False, True]):
        if mode == 0 and divided:
            payload, width = (31 if maximum else 0), 5
            raw = [(1, 1), (payload, width), (ratio, 3)]
            if ratio == 0:
                raw += [(payload, width)]
        else:
            width, offset = choices(mode == 1, divided)[selector]
            payload = 2**width - 1 if maximum else 0
            raw = [(divided, 1), (selector, 2), (payload, width), (ratio, 3)]
            if ratio == 0:
                raw += [(selector, 2), (payload, width)]
        yield make(f'raw-dimensions-{mode}-{divided}-{selector}-{ratio}-{maximum}', mode, (8, 8, 0, 0), raw_fields=raw)
    for n, d, loops, flag in product([1, 24, 100, 1000, 1024, 1025, 2**30], [1, 8, 256, 257, 1001, 1024],
                                     [0, 1, 7, 8, 65535, 65536, 2**32 - 1], range(2)):
        yield make(f'animation-{n}-{d}-{loops}-{flag}', 2, (n, d, loops, flag), 3)
    for selectors, maximum, flag in product(product(range(4), repeat=3), [False, True], range(2)):
        raw = []
        for selector, distribution in zip(selectors, [NUMERATOR, DENOMINATOR, LOOPS], strict=True):
            width, offset = distribution[selector]
            raw += [(selector, 2), (2**width - 1 if maximum else 0, width)]
        yield make(f'raw-animation-{selectors}-{maximum}-{flag}', 2, (100, 1, 0, 0), raw_fields=raw + [(flag, 1)])
    for mode in range(3):
        for length, fill in product(range(13), [0, 255]):
            case = make(f'byte-prefix-{mode}-{length}-{fill}', mode, (100, 1, 0, 0))
            case.update(data=bytes([fill]) * length, queries=[(p, length * 8) for p in range(min(9, length * 8 + 1))], canonical=False)
            yield case
        for budget in range(-1, 13):
            yield make(f'budget-{mode}-{budget}', mode, (1000, 1001, 2**32 - 1, 1), budget=budget)
        for prefix in [-1, 57, 2**31 - 1]:
            yield make(f'prefix-domain-{mode}-{prefix}', mode, (100, 1, 0, 0), prefix=prefix)
    for mode in range(2):
        for width, height in [(0, 1), (1, 0), (-1, 1), (1, -1), (2**32, 1), (1, 2**32), (1, 2**30 + 1), (1, 5441)]:
            yield make(f'domain-{mode}-{width}-{height}', mode, (width, height, 0, 0))
    for n, d in [(0, 1), (1, 0), (2**30 + 1, 1), (1, 1025)]:
        yield make(f'animation-domain-{n}-{d}', 2, (n, d, 0, 0))
    fixture = Path(__file__).with_name('fixtures') / 'jxl_headers_reference.json'
    record = json.loads(fixture.read_text())
    for row in record['images']:
        data = base64.b64decode(row['codestream_base64'], validate=True)
        if hashlib.sha256(data).hexdigest() != row['codestream_sha256'] or not data.startswith(b'\xff\x0a'):
            raise ValueError('reference fixture bytes differ')
        case = make(f'real-{row["width"]}-{row["height"]}', 0, (row['width'], row['height'], 0, 0))
        size = sum(w for _, w in fields(0, case['values'])['fields'])
        case.update(data=data, queries=[(16, e) for e in range(16, 16 + size + 1)] + [(16, len(data) * 8)], canonical=False)
        yield case


def fixture(case):
    return (struct.pack('<I4QiiII', case['mode'], *(v % 2**64 for v in case['values']), case['budget'], case['prefix'],
                        len(case['queries']), len(case['data']))
            + b''.join(struct.pack('<qq', *q) for q in case['queries']) + case['data'])


def check_worker(stdout, stderr, selected):
    wanted = []
    for index, case in enumerate(selected):
        result = encoded(case)
        wanted.append(f'{index} pack error {result["error"]}' if 'error' in result
                      else f'{index} pack ok {result["bits"]} {result["data"].hex()}')
        wanted += [f'{index} query {i} ' + decode(case['data'], p, e, case['mode']) for i, (p, e) in enumerate(case['queries'])]
    heaps = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
    if stdout.splitlines() != wanted or not heaps or not all(int(a) > 0 and a == b and c == '0' for a, b, c in heaps):
        raise ValueError('header results or heap evidence differ')


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
    repo, reference = Path(__file__).resolve().parents[2], args.reference_source.resolve()
    if {p: digest(reference / p) for p in UPSTREAM} != UPSTREAM:
        raise ValueError('offline reference sources differ')
    compiler, cxx = args.fern.resolve(), Path(shutil.which(args.cxx) or args.cxx).resolve()
    sources = ['fern-image/packages/codecs/jxl/' + p + '.fern' for p in ['bits', 'u32_fields', 'headers']]
    sources += ['fern-image/tests/' + p for p in ['jxl_headers_corpus.fern', 'reference_jxl_headers.cc', 'qualify_jxl_headers.py',
                                                'qualify_jxl_bits.py', 'qualify_jxl_integers.py', 'fixtures/jxl_headers_reference.json']]
    frozen, tools = {p: digest(repo / p) for p in sources}, dict(compiler=digest(compiler), cxx=digest(cxx))
    record = dict(complete=False, target=args.target, source_inventory=frozen, tool_sha256=tools,
                  upstream_sha256=UPSTREAM, commands=[], observations=[])
    env = dict(os.environ, FERN_LEAKCHECK='1')
    def run(label, command, data=None, cwd=out):
        result = subprocess.run(list(map(str, command)), input=data, cwd=cwd, env=env, capture_output=True, timeout=120)
        for stream in ['stdout', 'stderr']:
            (out / (label + '.' + stream)).write_bytes(getattr(result, stream))
        record['commands'].append(dict(label=label, command=list(map(str, command)), exit_code=result.returncode,
                                       stdout_sha256=digest(out / (label + '.stdout')), stderr_sha256=digest(out / (label + '.stderr'))))
        if result.returncode or label.startswith(('build-', 'batch-')):
            (out / 'proof.json').write_text(json.dumps(record, indent=2) + '\n')
        result.check_returncode()
        return result.stdout.decode(), result.stderr.decode()
    common = [cxx, '-std=c++17', '-O2', '-DNDEBUG', '-Wall', '-Wextra', '-Werror', '-ffunction-sections', '-fdata-sections',
              '-isystem', reference, '-isystem', reference / 'lib/include', '-isystem', reference / 'highway']
    objects = []
    for name, source, flags in [('adapter', repo / 'fern-image/tests/reference_jxl_headers.cc', []),
                                ('headers', reference / 'lib/jxl/headers.cc', []),
                                ('fields', reference / 'lib/jxl/fields.cc', ['-Wno-unused-parameter']),
                                ('reader', reference / 'lib/jxl/dec_bit_reader.cc', [])]:
        obj = out / (name + '.o')
        run('build-' + name, [*common, *flags, '-c', source, '-o', obj])
        objects.append(obj)
    oracle = out / 'reference'
    run('build-reference', [cxx, *objects, '-Wl,-dead_strip' if platform.system() == 'Darwin' else '-Wl,--gc-sections', '-o', oracle])
    binary = out / ('worker.wasm' if args.target == 'wasm32-wasi' else 'worker')
    run('build-worker', [compiler, '-target', args.target, '-sanitize', '-o', binary,
                         repo / 'fern-image/tests/jxl_headers_corpus.fern', repo / 'internal/stdlib'], cwd=repo)
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
            result = encoded(case)
            row = dict(name=case['name'], input_sha256=digest(path), queries=len(case['queries']))
            if 'data' in result:
                packed = path.with_name(path.name + '.packed')
                if packed.read_bytes() != result['data']:
                    raise ValueError('complete header bytes differ')
                row['packed_sha256'] = digest(packed)
            positions = sorted({p for p, _ in case['queries'] if 0 <= p <= len(case['data']) * 8})
            command = ''.join(f'read {case["mode"]} {p}\n' for p in positions)
            expected = [decode(case['data'], p, len(case['data']) * 8, case['mode']).replace('error Input(Truncated)', 'truncated') for p in positions]
            baseline = fields(case['mode'], case['values'], fallback=False)
            baseline_bytes = baseline if 'error' in baseline else pack(baseline['fields'], 20)
            expected.append('value' if 'error' in baseline_bytes else f'encoded {baseline_bytes["bits"]} {baseline_bytes["data"].hex()}')
            command += f'encode {case["mode"]} ' + ' '.join(str(v % 2**64) for v in case['values']) + '\n'
            fallback = case['mode'] == 1 and 'error' in baseline and 'fields' in fields(case['mode'], case['values'])
            if fallback and case['canonical']:
                standalone = pack(fields(case['mode'], case['values'])['fields'], 20)
                expected.append(f'encoded {standalone["bits"]} {standalone["data"].hex()}')
                command += f'rewrite 1 {max(0, min(7, case["prefix"]))}\n'
                row['reference_preview_setup_limit'] = True
            raw = out / f'raw-{i}.bin'
            raw.write_bytes(case['data'])
            actual, _ = run('reference-' + str(i), [oracle, raw], command.encode())
            if actual.splitlines() != expected:
                raise ValueError('pinned header values, positions or canonical bytes differ')
            row.update(reference_reads=len(positions), reference_encodes=1, real_codestream=case['name'].startswith('real-'))
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
