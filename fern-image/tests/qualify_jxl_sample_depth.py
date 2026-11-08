"""Check Fern sample precision with equations and original offline classes."""

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
from qualify_jxl_integers import fields32, decode as integer_read

SOURCE_MANIFEST = Path(__file__).with_name('fixtures') / 'jxl_sample_depth_sources.json'
UPSTREAM = json.loads(SOURCE_MANIFEST.read_text())


def choices(floating):
    return [(0, value) for value in ([32, 16, 24] if floating else [8, 10, 12])] + [(6, 1)]


def valid(floating, precision, exponent):
    if not floating:
        return 1 <= precision <= 31
    return 2 <= exponent <= 8 and 2 <= precision - exponent - 1 <= 23


def fields(floating, precision, exponent):
    if not valid(floating, precision, exponent):
        return dict(error='Value')
    result = [(int(floating), 1), *fields32(precision, choices(floating))['fields']]
    return dict(fields=result + ([(exponent - 1, 4)] if floating else []))


def decode(data, position, extent):
    def advance(result):
        nonlocal position
        if result.startswith('error'):
            error = result.removeprefix('error ')
            raise ValueError(error if error.startswith('Input(') else 'Input(' + error + ')')
        _, value, next_bit = result.split()
        position = int(next_bit)
        return int(value)
    try:
        floating = advance(read(data, position, 1, extent))
        precision = advance(integer_read(data, position, extent, 1, choices(floating)))
        exponent = advance(read(data, position, 4, extent)) + 1 if floating else 0
        if not valid(floating, precision, exponent):
            return 'error Value'
        return f'depth {floating} {precision} {exponent} {position}'
    except ValueError as error:
        return 'error ' + str(error)


def encoded(case):
    result = fields(*case['values'])
    return result if 'error' in result else pack([(0, case['prefix']), *result['fields']], case['budget'])


def make(name, floating, precision, exponent, prefix=0, raw_fields=None, budget=4):
    values = (int(floating), precision, exponent)
    result = fields(*values)
    raw = raw_fields if raw_fields is not None else result.get('fields', [(0, 16)])
    offset = max(0, min(7, prefix))
    packed = pack([(0, offset), *raw], 16)
    data, length = packed['data'], packed['bits']
    queries = [(offset, extent) for extent in range(length + 1)]
    queries += [(p, len(data) * 8) for p in range(min(9, len(data) * 8 + 1))]
    queries += [(-1, length), (length + 1, length), (0, -1), (0, len(data) * 8 + 1), (2**63 - 1, length)]
    return dict(name=name, values=values, prefix=prefix, budget=budget, data=data, queries=queries)


def cases():
    for precision, exponent, prefix in product(range(1, 32), [0, 2**32 - 1], range(8)):
        yield make(f'integer-{precision}-{exponent}-{prefix}', False, precision, exponent, prefix)
    for exponent, mantissa, prefix in product(range(2, 9), range(2, 24), range(8)):
        precision = exponent + mantissa + 1
        yield make(f'float-{precision}-{exponent}-{prefix}', True, precision, exponent, prefix)
    for floating, selector in product(range(2), range(4)):
        width, offset = choices(floating)[selector]
        for payload, exponent in product(range(2**width), range(1, 17) if floating else [0]):
            raw = [(floating, 1), (selector, 2), (payload, width)]
            if floating:
                raw += [(exponent - 1, 4)]
            yield make(f'raw-{floating}-{selector}-{payload}-{exponent}', floating, 8 if not floating else 16,
                       0 if not floating else 5, raw_fields=raw)
    for floating, precision, exponent in product(range(2), [0, 1, 2, 3, 4, 31, 32, 33, 64, 65, 2**32 - 1],
                                                 [0, 1, 2, 5, 8, 9, 16, 2**32 - 1]):
        if not valid(floating, precision, exponent):
            yield make(f'invalid-{floating}-{precision}-{exponent}', floating, precision, exponent)
    for floating in range(2):
        for budget in range(-1, 5):
            yield make(f'budget-{floating}-{budget}', floating, 31 if not floating else 29, 7, prefix=7, budget=budget)
        for prefix in [-1, 57, 2**31 - 1]:
            yield make(f'prefix-{floating}-{prefix}', floating, 8 if not floating else 16, 5, prefix=prefix)
    for length, fill in product(range(5), [0, 255, 37, 128]):
        case = make(f'physical-{length}-{fill}', False, 8, 0)
        case.update(data=bytes([fill]) * length, queries=list(product(range(length * 8 + 1), range(length * 8 + 1))))
        yield case
    from generate_jxl_sample_depth import codestream
    frozen = json.loads(Path(__file__).with_name('fixtures').joinpath('jxl_sample_depth_reference.json').read_text())
    for row in frozen['images']:
        original = base64.b64decode(row['file_base64'], validate=True)
        data = base64.b64decode(row['codestream_base64'], validate=True)
        if hashlib.sha256(original).hexdigest() != row['file_sha256'] or hashlib.sha256(data).hexdigest() != row['codestream_sha256']:
            raise ValueError('real reference fixture hashes differ')
        if codestream(original) != data:
            raise ValueError('real reference codestream differs')
        case = make(f'real-{row["bits"]}-{row["exponent"]}', row['exponent'] != 0, row['bits'], row['exponent'])
        size = sum(width for _, width in fields(*case['values'])['fields'])
        position = row['position']
        case.update(data=data, queries=[(position, e) for e in range(position, position + size + 1)] + [(position, len(data) * 8)])
        yield case


def fixture(case):
    return (struct.pack('<3IiiII', *case['values'], case['budget'], case['prefix'], len(case['queries']), len(case['data']))
            + b''.join(struct.pack('<qq', *query) for query in case['queries']) + case['data'])


def check_worker(stdout, stderr, selected):
    wanted = []
    for index, case in enumerate(selected):
        result = encoded(case)
        wanted.append(f'{index} pack error {result["error"]}' if 'error' in result
                      else f'{index} pack ok {result["bits"]} {result["data"].hex()}')
        wanted += [f'{index} query {i} ' + decode(case['data'], p, e) for i, (p, e) in enumerate(case['queries'])]
    heaps = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
    if stdout.splitlines() != wanted or not heaps or not all(int(a) > 0 and a == b and c == '0' for a, b, c in heaps):
        raise ValueError('sample precision results or heap evidence differ')


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
    sources = ['fern-image/packages/codecs/jxl/' + p + '.fern' for p in ['bits', 'u32_fields', 'sample_depth']]
    sources += ['fern-image/tests/' + p for p in ['jxl_sample_depth_corpus.fern', 'reference_jxl_sample_depth.cc',
        'qualify_jxl_sample_depth.py', 'qualify_jxl_bits.py', 'qualify_jxl_integers.py', 'fixtures/jxl_sample_depth_sources.json',
        'fixtures/jxl_sample_depth_reference.json', 'generate_jxl_sample_depth.py', 'generate_jxl_sample_depth.c',
        'generate_jxl_headers.py', 'qualify_jxl_headers.py', 'qualify_jxl_sample_depth_test.py', 'jxl_sample_depth_test.fern']]
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
    for name, source, flags in [('adapter', repo / 'fern-image/tests/reference_jxl_sample_depth.cc', []),
                              ('metadata', reference / 'lib/jxl/image_metadata.cc', ['-Wno-unused-parameter']),
                              ('fields', reference / 'lib/jxl/fields.cc', ['-Wno-unused-parameter']),
                              ('reader', reference / 'lib/jxl/dec_bit_reader.cc', [])]:
        obj = out / (name + '.o')
        run('build-' + name, [*common, *flags, '-c', source, '-o', obj])
        objects.append(obj)
    oracle = out / 'reference'
    run('build-reference', [cxx, *objects, '-Wl,-dead_strip' if platform.system() == 'Darwin' else '-Wl,--gc-sections', '-o', oracle])
    binary = out / ('worker.wasm' if args.target == 'wasm32-wasi' else 'worker')
    run('build-worker', [compiler, '-target', args.target, '-sanitize', '-o', binary,
        repo / 'fern-image/tests/jxl_sample_depth_corpus.fern', repo / 'internal/stdlib'], cwd=repo)
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
                    raise ValueError('complete precision bytes differ')
                row['packed_sha256'] = digest(packed)
            positions = sorted({p for p, _ in case['queries'] if 0 <= p <= len(case['data']) * 8})
            command = ''.join(f'read {p}\n' for p in positions)
            expected = [decode(case['data'], p, len(case['data']) * 8) for p in positions]
            expected = ['invalid' if value.startswith('error') else value for value in expected]
            standalone = fields(*case['values'])
            standalone = standalone if 'error' in standalone else pack(standalone['fields'], 4)
            expected.append('value' if 'error' in standalone else f'encoded {standalone["bits"]} {standalone["data"].hex()}')
            command += 'encode ' + ' '.join(map(str, case['values'])) + '\n'
            raw = out / f'raw-{i}.bin'
            raw.write_bytes(case['data'])
            actual, _ = run('reference-' + str(i), [oracle, raw], command.encode())
            if actual.splitlines() != expected:
                raise ValueError('pinned precision values, positions or canonical bytes differ')
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
