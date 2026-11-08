"""Check Fern integer fields with independent equations and pinned coders."""

import argparse
from itertools import islice
import json
import os
from pathlib import Path
import platform
import re
import shutil
import struct
import subprocess

from qualify_jxl_bits import UPSTREAM as BIT_SOURCES, digest, pack, read

UPSTREAM = dict(BIT_SOURCES, **{
    'lib/jxl/fields.cc': 'dbdca19821025ae4d5eb5eef6c3497a66b13d6bd81272eaca91428cb7fb9407b',
    'lib/jxl/fields.h': '7280a1c6842d12e7c3445fc35e2cb666d5f006c6de0f696cf6ed3c251879ddc3',
    'lib/jxl/field_encodings.h': 'afa553a74fb65c7b958bea0ae24f464a23ca0d3903e06750bc349c5b5c258080',
    'lib/jxl/base/bits.h': '10a3c4ac1424e2e770fd5eae361c335fea9fb48d95000f9912febc3e759ab204',
    'lib/jxl/base/printf_macros.h': 'e695781f9363d3745d6819e8f9c6e4d680aaf0a28d2f6c3661534c174d0e92c3',
    'highway/hwy/base.h': 'e3f6049a91809d443841ffa7ac29b3b0e88827e429b37f54434cb18d3e1edd64',
    'highway/hwy/detect_compiler_arch.h': '2efa39d49fab0800a0dd5f91821df02a3cd86a008eebd68ac98c5249cd4fa85d',
    'highway/hwy/highway_export.h': '8a883413b083c721f879163780630aa3ec741b386b3db6d05ac042292b904d45',
    'highway/LICENSE': 'e340270d4f64384569a91d546acb5b094d69ce47f0c015db77abb74dc6f815af',
})
DEFAULT = [(0, 0), (0, 1), (4, 2), (32, 0)]


def valid(distributions):
    return len(distributions) == 4 and all(0 <= w <= 32 and 0 <= o <= (2**31 - 1 if w == 0 else 2**26 - 1)
                                           for w, o in distributions)


def fields32(value, distributions):
    if not valid(distributions):
        return dict(error='Distribution')
    choices = [(w, i, o) for i, (w, o) in enumerate(distributions)
               if (w == 0 and value == o) or (w > 0 and o <= value < o + 2**w)]
    if not choices:
        return dict(error='Value')
    width, selector, offset = min(choices)
    return dict(fields=[(selector, 2), (value - offset, width)])


def fields64(value):
    if value == 0:
        return [(0, 2)]
    if value <= 16:
        return [(1, 2), (value - 1, 4)]
    if value <= 272:
        return [(2, 2), (value - 17, 8)]
    result = [(3, 2), (value % 4096, 12)]
    for shift in range(12, 61, 8):
        width = min(8, 64 - shift)
        if value < 2**shift:
            return result + [(0, 1)]
        result += [(1, 1), ((value // 2**shift) % 2**width, width)]
    return result


def decode(data, position, extent, mode, distributions):
    if mode and not valid(distributions):
        return 'error Distribution'
    def take(width):
        nonlocal position
        result = read(data, position, width, extent)
        if result.startswith('error'):
            error = result.split()[1]
            raise ValueError('Input(' + error + ')' if mode else error)
        _, value, next_bit = result.split()
        position = int(next_bit)
        return int(value)
    try:
        selector = take(2)
        if mode:
            width, offset = distributions[selector]
            value = (take(width) + offset) % 2**32
        elif selector == 0:
            value = 0
        elif selector < 3:
            value = take(4 if selector == 1 else 8) + (1 if selector == 1 else 17)
        else:
            value = take(12)
            for shift in range(12, 61, 8):
                if not take(1):
                    break
                value += take(min(8, 64 - shift)) * 2**shift
        return f'ok {value} {position}'
    except ValueError as error:
        return 'error ' + str(error)


def encoded(case):
    result = fields32(case['value'], case['distributions']) if case['mode'] else dict(fields=fields64(case['value']))
    if 'error' in result:
        return result
    return pack([(0, case['prefix']), *result['fields']], case['budget'])


def make(name, mode, value, distributions=DEFAULT, prefix=0, raw_fields=None, budget=12):
    code = fields32(value, distributions) if mode else dict(fields=fields64(value))
    fields = raw_fields if raw_fields is not None else code.get('fields', [(3, 2), (255, 8)])
    result = pack([(0, prefix), *fields], 20)
    data, length = result['data'], result['bits']
    queries = [(prefix, extent) for extent in range(length + 1)]
    queries += [(p, len(data) * 8) for p in range(min(8, len(data) * 8 + 1))]
    queries += [(p, e) for p, e in [(-1, length), (length + 1, length), (0, -1), (0, len(data) * 8 + 1),
                                     (len(data) * 8, len(data) * 8), (2**63 - 1, length)]]
    return dict(name=name, mode=mode, value=value, distributions=distributions, prefix=prefix,
                data=data, queries=queries, budget=budget)


def cases():
    values = set(range(274)) | {2**64 - 1}
    values |= {2**b + delta for b in range(1, 64) for delta in [-1, 0, 1]}
    for value in sorted(values):
        for prefix in range(8):
            yield make(f'u64-{value}-{prefix}', 0, value, prefix=prefix)
    for groups in range(8):
        for payload in [0, 1]:
            fields = [(3, 2), (payload, 12)]
            for i in range(groups):
                fields += [(1, 1), (payload, 4 if i == 6 else 8)]
            if groups < 7:
                fields += [(0, 1)]
            for prefix in range(8):
                yield make(f'u64-long-{groups}-{payload}-{prefix}', 0, 0, prefix=prefix, raw_fields=fields)
    for width in range(1, 33):
        for offset in [0, 1, 2**26 - 1]:
            distributions = [(width, offset), (0, 17), (0, 23), (0, 31)]
            for payload in [0, (2**width - 1) // 3, 2**width - 1]:
                for prefix in [0, 7]:
                    value = (payload + offset) % 2**32
                    yield make(f'u32-{width}-{offset}-{payload}-{prefix}', 1, value, distributions, prefix,
                               [(0, 2), (payload, width)])
    profiles = [[(8, 0), (2, 0), (2, 0), (0, 2)], [(0, 2**31 - 1), (0, 9), (0, 9), (32, 0)], DEFAULT]
    for i, profile in enumerate(profiles):
        for value in [0, 1, 2, 3, 4, 9, 17, 255, 256, 2**31 - 1, 2**32 - 1]:
            for prefix in range(8):
                yield make(f'choice-{i}-{value}-{prefix}', 1, value, profile, prefix)
    for i, bad in enumerate([(-1, 0), (33, 0), (0, 2**31), (1, 2**26)]):
        for selector in range(4):
            profile = DEFAULT.copy()
            profile[selector] = bad
            yield make(f'bad-{i}-{selector}', 1, 0, profile)
    for mode in [0, 1]:
        for budget in range(-1, 12):
            yield make(f'budget-{mode}-{budget}', mode, 2**64 - 1 if mode == 0 else 2**32 - 1, budget=budget)
        for size in range(11):
            case = make(f'prefix-{mode}-{size}', mode, 0)
            case.update(data=b'\xff' * size, queries=[(p, size * 8) for p in range(min(9, size * 8 + 1))])
            yield case


def fixture(case):
    return (struct.pack('<IQiiII', case['mode'], case['value'], case['budget'], case['prefix'],
                        len(case['queries']), len(case['data']))
            + b''.join(struct.pack('<iI', *d) for d in case['distributions'])
            + b''.join(struct.pack('<qq', *q) for q in case['queries']) + case['data'])


def check_worker(stdout, stderr, selected):
    wanted = []
    for index, case in enumerate(selected):
        result = encoded(case)
        wanted.append(f'{index} pack error {result["error"]}' if 'error' in result
                      else f'{index} pack ok {result["bits"]} {result["data"].hex()}')
        wanted += [f'{index} query {i} ' + decode(case['data'], p, e, case['mode'], case['distributions'])
                   for i, (p, e) in enumerate(case['queries'])]
    heaps = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
    if stdout.splitlines() != wanted or not heaps or not all(int(a) > 0 and a == b and c == '0' for a, b, c in heaps):
        raise ValueError('integer results or heap evidence differ')


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
        raise ValueError('offline reference sources differ')
    compiler = args.fern.resolve()
    cxx = Path(shutil.which(args.cxx) or args.cxx).resolve()
    sources = ['fern-image/packages/codecs/jxl/' + p + '.fern' for p in ['bits', 'u32_fields', 'u64_fields']]
    sources += ['fern-image/tests/' + p for p in ['jxl_integers_corpus.fern', 'reference_jxl_integers.cc',
                                                'qualify_jxl_integers.py', 'qualify_jxl_bits.py']]
    frozen = {p: digest(repo / p) for p in sources}
    tools = dict(compiler=digest(compiler), cxx=digest(cxx))
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
    for name, source, flags in [('adapter', repo / 'fern-image/tests/reference_jxl_integers.cc', []),
                                ('fields', reference / 'lib/jxl/fields.cc', ['-Wno-unused-parameter']),
                                ('reader', reference / 'lib/jxl/dec_bit_reader.cc', [])]:
        obj = out / (name + '.o')
        run('build-' + name, [*common, *flags, '-c', source, '-o', obj])
        objects.append(obj)
    oracle = out / 'reference'
    strip = '-Wl,-dead_strip' if platform.system() == 'Darwin' else '-Wl,--gc-sections'
    run('build-reference', [cxx, *objects, strip, '-o', oracle])
    binary = out / ('worker.wasm' if args.target == 'wasm32-wasi' else 'worker')
    run('build-worker', [compiler, '-target', args.target, '-sanitize', '-o', binary,
                         repo / 'fern-image/tests/jxl_integers_corpus.fern', repo / 'internal/stdlib'], cwd=repo)
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
                    raise ValueError('complete integer bytes differ')
                row['packed_sha256'] = digest(packed)
            if case['mode'] == 0 or valid(case['distributions']):
                config = ''.join(f' {w} {o}' for w, o in case['distributions']) if case['mode'] else ''
                positions = sorted({p for p, _ in case['queries'] if 0 <= p <= len(case['data']) * 8})
                command = ''.join(f'u{32 if case["mode"] else 64} {p}{config}\n' for p in positions)
                expected = [decode(case['data'], p, len(case['data']) * 8, case['mode'], case['distributions'])
                            .replace('error Truncated', 'truncated').replace('error Input(Truncated)', 'truncated') for p in positions]
                if case['mode']:
                    choice = fields32(case['value'], case['distributions'])
                    expected.append('value' if 'error' in choice else f'choice {choice["fields"][0][0]} {2 + choice["fields"][1][1]}')
                    command += f'choose32 {case["value"]}{config}\n'
                else:
                    expected.append('size ' + str(sum(w for _, w in fields64(case['value']))))
                    command += f'size64 {case["value"]}\n'
                raw = out / f'raw-{i}.bin'
                raw.write_bytes(case['data'])
                actual, _ = run('reference-' + str(i), [oracle, raw], command.encode())
                if actual.splitlines() != expected:
                    raise ValueError('pinned integer values, positions or canonical choices differ')
                row['reference_reads'] = len(positions)
                row['reference_choices'] = 1
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
