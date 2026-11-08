"""Check complete tone descriptors with original classes and independent values."""

import argparse
import base64
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
from qualify_jxl_f16 import decode as finite_read, encode_word, float_bits, half_bits

UPSTREAM = json.loads(Path(__file__).with_name('fixtures').joinpath('jxl_tone_sources.json').read_text())
DEFAULT = (float_bits(255.0), 0, 0, 0)


def samples(values):
    a, b, relative, c = values
    return [struct.unpack('<f', struct.pack('<I', word))[0] for word in [a, b, c]]


def valid(values):
    a, b, c = samples(values)
    return a > 0 and 0 <= b <= a and c >= 0 and (not values[2] or c <= 1)


def raw_fields(intensity, minimum, relative, linear):
    return [(0, 1), (intensity, 16), (minimum, 16), (relative, 1), (linear, 16)]


def fields(values):
    if not valid(values):
        return dict(error='Value')
    a, b, relative, c = values
    words = list(map(encode_word, [a, b, c]))
    if None in words:
        return dict(error='Float(Value)')
    if words[0] == 0:
        return dict(error='Value')
    if words == [0x5bf8, 0, 0] and not relative:
        return dict(fields=[(1, 1)])
    return dict(fields=raw_fields(words[0], words[1], relative, words[2]))


def original_fields(values):
    a, b, c = samples(values)
    relative = values[2]
    epsilon = struct.unpack('<f', struct.pack('<f', 1e-6))[0]
    # The release default visitor retains its partial result after an invalid relation.
    default = abs(a - 255) < epsilon
    if not a <= 0:
        default &= abs(b) < epsilon
        if not (b < 0 or b > a):
            default &= not relative and abs(c) < epsilon
    if default:
        return dict(fields=[(1, 1)])
    if not valid(values):
        return dict(error='Value')
    words = list(map(encode_word, [values[0], values[1], values[3]]))
    return dict(error='Value') if None in words else dict(fields=raw_fields(words[0], words[1], relative, words[2]))


def decode(data, position, extent):
    def flag():
        nonlocal position
        result = read(data, position, 1, extent)
        if result.startswith('error'):
            raise ValueError('Input(' + result.removeprefix('error ') + ')')
        _, value, position = result.split()
        position = int(position)
        return int(value)
    def number():
        nonlocal position
        result = finite_read(data, position, extent)
        if result.startswith('error'):
            raise ValueError('Float(' + result.removeprefix('error ') + ')')
        _, value, position = result.split()
        position = int(position)
        return int(value)
    try:
        if flag():
            return 'tone ' + ' '.join(map(str, [*DEFAULT, position]))
        a = number()
        av = struct.unpack('<f', struct.pack('<I', a))[0]
        if av <= 0:
            return 'error Value'
        b = number()
        bv = struct.unpack('<f', struct.pack('<I', b))[0]
        if bv < 0 or bv > av:
            return 'error Value'
        relative, c = flag(), number()
        values = (a, b, relative, c)
        return 'tone ' + ' '.join(map(str, [*values, position])) if valid(values) else 'error Value'
    except ValueError as error:
        return 'error ' + str(error)


def encoded(values, prefix=0, budget=7):
    result = fields(values)
    return result if 'error' in result else pack([(0, prefix), *result['fields']], budget)


def cases():
    writers = [DEFAULT, (float_bits(1000), float_bits(0.5), 1, float_bits(0.25)),
        (float_bits(255), float_bits(1e-7), 0, 0), (float_bits(1e-8), 0, 0, 0),
        (float_bits(255), float_bits(-1e-7), 1, float_bits(10)), (0x7f800000, 0, 0, 0)]
    for offset in range(8):
        data = pack([(0, offset), *raw_fields(0x63d0, 0x3800, 1, 0x3400)], 8)['data']
        queries = [(offset, cut) for cut in range(offset + 51)]
        queries += [(-1, 50), (0, -1), (0, len(data) * 8 + 1), (2**63 - 1, len(data) * 8)]
        encodes = [(value, prefix, budget) for value, prefix, budget in product(writers, [offset, -1, 56, 57], [-1, 0, 1, 6, 7, 8, 14])]
        yield dict(name=f'bounds-{offset}', data=data, queries=queries, encodes=encodes)
    for length, fill in product(range(8), [0, 255, 128, 37]):
        data = bytes([fill]) * length
        yield dict(name=f'physical-{length}-{fill}', data=data,
            queries=list(product(range(length * 8 + 1), range(length * 8 + 1))), encodes=[(DEFAULT, 0, 1)])
    for axis, relative in product(range(3), range(2)):
        for start in range(0, 65536, 256):
            offset = start // 256 % 8
            raw, encodes = [], []
            for word in range(start, start + 256):
                words = [0x7bff if axis == 1 else 0x3c00, 0, 0]
                words[axis] = word
                raw += raw_fields(words[0], words[1], relative, words[2])
                values = [half_bits(w) if half_bits(w) is not None else 0x7fc00000 for w in words]
                encodes.append(((values[0], values[1], relative, values[2]), offset, 8))
            data = pack([(0, offset), *raw], 1601)['data']
            queries = [(offset + i * 50, offset + (i + 1) * 50) for i in range(256)]
            yield dict(name=f'raw-{axis}-{relative}-{start}', data=data, queries=queries, encodes=encodes)
    values = [0, 0x80000000, 1, 0x80000001, 0x337fffff, 0xb37fffff, 0x33800000, 0xb3800000, 0x38800000,
        float_bits(0.5), float_bits(1), 0x3f800001, float_bits(255), float_bits(1000),
        0x477fe000, 0x477fe001, 0x7f800000, 0x7fc00000, 0xbf800000]
    combinations = list(product(values, values, range(2), values))
    for start in range(0, len(combinations), 256):
        yield dict(name=f'relations-{start}', data=b'', queries=[],
            encodes=[(row, (start + i) % 8, 8) for i, row in enumerate(combinations[start:start + 256])])
    from generate_jxl_f16 import positions
    from generate_jxl_sample_depth import codestream
    frozen = json.loads(Path(__file__).with_name('fixtures').joinpath('jxl_f16_reference.json').read_text())
    import hashlib
    for index, row in enumerate(frozen['images']):
        original = base64.b64decode(row['file_base64'], validate=True)
        data = base64.b64decode(row['codestream_base64'], validate=True)
        if hashlib.sha256(original).hexdigest() != row['file_sha256'] or hashlib.sha256(data).hexdigest() != row['codestream_sha256']:
            raise ValueError('real tone fixture hashes differ')
        if codestream(original) != data or positions(data) != row['positions']:
            raise ValueError('real tone fixture profile differs')
        at = row['positions'][0] - 1
        yield dict(name=f'real-{index}', data=data, queries=[(at, e) for e in range(at, at + 51)],
            encodes=[((row['input_bits'][0], row['input_bits'][1], 0, row['input_bits'][2]), 0, 7)])


def fixture(case):
    return (struct.pack('<III', len(case['queries']), len(case['encodes']), len(case['data']))
        + b''.join(struct.pack('<qq', *query) for query in case['queries'])
        + b''.join(struct.pack('<4Iii', *values, prefix, budget) for values, prefix, budget in case['encodes']) + case['data'])


def check_worker(stdout, stderr, selected):
    wanted = []
    for index, case in enumerate(selected):
        wanted += [f'{index} query {i} ' + decode(case['data'], p, e) for i, (p, e) in enumerate(case['queries'])]
        for i, (values, prefix, budget) in enumerate(case['encodes']):
            result = encoded(values, prefix, budget)
            wanted.append(f'{index} encode {i} error {result["error"]}' if 'error' in result
                else f'{index} encode {i} ok {result["bits"]} {result["data"].hex()}')
    heaps = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
    if stdout.splitlines() != wanted or not heaps or not all(int(a) > 0 and a == b and c == '0' for a, b, c in heaps):
        raise ValueError('tone results or heap evidence differ')


def reference_queries(case):
    commands = 'input ' + (case['data'].hex() or '-') + '\n'
    wanted = []
    positions = sorted({p for p, _ in case['queries'] if 0 <= p <= len(case['data']) * 8})
    for p in positions:
        commands += f'read {p}\n'
        result = decode(case['data'], p, len(case['data']) * 8)
        wanted.append('invalid' if result.startswith('error') else result)
    counts = dict(reads=len(positions), encodes=0, safe_reads=0, exact_default=0, refused_unreadable=0, refused_invalid=0)
    for values, _, _ in case['encodes']:
        commands += 'encode ' + ' '.join(map(str, values)) + '\n'
        original, ours = original_fields(values), fields(values)
        counts['encodes'] += 1
        if 'error' in original:
            wanted.append('value')
        else:
            output = pack(original['fields'], 7)
            wanted.append(f'encoded {output["bits"]} {output["data"].hex()}')
            result = decode(output['data'], 0, output['bits'])
            wanted.append('invalid' if result.startswith('error') else result)
            if 'error' in ours:
                counts['refused_unreadable' if result.startswith('error') else 'refused_invalid'] += 1
            elif original != ours:
                counts['exact_default'] += 1
        if 'error' not in ours:
            output = pack(ours['fields'], 7)
            commands += 'input ' + output['data'].hex() + '\nread 0\n'
            result = decode(output['data'], 0, output['bits'])
            if result.startswith('error'):
                raise ValueError('Fern output model is unreadable')
            wanted.append(result)
            counts['safe_reads'] += 1
    return commands.encode(), wanted, counts

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
    sources = ['fern-image/packages/codecs/jxl/' + p + '.fern' for p in ['bits', 'f16_fields', 'tone_metadata']]
    sources += ['fern-image/tests/' + p for p in ['jxl_tone_corpus.fern', 'reference_jxl_tone.cc', 'qualify_jxl_tone.py',
        'qualify_jxl_bits.py', 'fixtures/jxl_tone_sources.json', 'jxl_tone_metadata_test.fern', 'qualify_jxl_tone_test.py',
        'fixtures/jxl_f16_sources.json',
        'fixtures/jxl_f16_reference.json', 'generate_jxl_f16.c', 'generate_jxl_f16.py', 'qualify_jxl_f16.py',
        'generate_jxl_headers.py', 'generate_jxl_sample_depth.py', 'qualify_jxl_headers.py', 'qualify_jxl_integers.py']]
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
    units = [('adapter', repo / 'fern-image/tests/reference_jxl_tone.cc', [])]
    units += [(name, reference / 'lib/jxl' / source, flags) for name, source, flags in [
        ('metadata', 'image_metadata.cc', ['-Wno-unused-parameter']), ('frame', 'frame_header.cc', ['-Wno-unused-parameter']), ('fields', 'fields.cc', ['-Wno-unused-parameter']), ('reader', 'dec_bit_reader.cc', []),
        ('encoder', 'enc_fields.cc', ['-Wno-unused-parameter']), ('writer', 'enc_bit_writer.cc', ['-Wno-unused-parameter']),
        ('memory', 'memory_manager_internal.cc', ['-Wno-unused-parameter'])]]
    for name, source, flags in units:
        obj = out / (name + '.o')
        run('build-' + name, [*common, *flags, '-c', source, '-o', obj])
        objects.append(obj)
    oracle = out / 'reference'
    run('build-reference', [cxx, *objects, '-Wl,-dead_strip' if platform.system() == 'Darwin' else '-Wl,--gc-sections', '-o', oracle])
    binary = out / ('worker.wasm' if args.target == 'wasm32-wasi' else 'worker')
    run('build-worker', [compiler, '-target', args.target, '-sanitize', '-o', binary,
        repo / 'fern-image/tests/jxl_tone_corpus.fern', repo / 'internal/stdlib'], cwd=repo)
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
        for case, path in zip(batch, paths, strict=True):
            commands, expected, counts = reference_queries(case)
            stdout, _ = run('reference-' + str(len(record['observations'])), [oracle], commands)
            if stdout.splitlines() != expected:
                raise ValueError('original tone class results differ')
            record['observations'].append(dict(name=case['name'], input_sha256=digest(path), queries=len(case['queries']),
                **counts))
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
