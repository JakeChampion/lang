"""Check finite metadata fields against original coders and ordered half values."""

import argparse
import base64
from bisect import bisect_right
from itertools import islice, product
import json
import math
import os
from pathlib import Path
import platform
import re
import shutil
import struct
import subprocess

from qualify_jxl_bits import digest, pack, read

UPSTREAM = json.loads(Path(__file__).with_name('fixtures').joinpath('jxl_f16_sources.json').read_text())
HALVES = [struct.unpack('<e', struct.pack('<H', word))[0] for word in range(31744)]


def float_bits(value):
    return struct.unpack('<I', struct.pack('<f', value))[0]


def half_bits(word):
    if word & 0x7c00 == 0x7c00:
        return None
    return float_bits(struct.unpack('<e', struct.pack('<H', word))[0])


def encode_word(word):
    value = struct.unpack('<f', struct.pack('<I', word))[0]
    if not math.isfinite(value) or abs(value) > HALVES[-1]:
        return None
    index = bisect_right(HALVES, abs(value)) - 1
    return index | (0x8000 if value < 0 and index else 0)


def decode(data, position, extent):
    raw = read(data, position, 16, extent)
    if raw.startswith('error'):
        return 'error Input(' + raw.removeprefix('error ') + ')'
    _, word, next_bit = raw.split()
    value = half_bits(int(word))
    return 'error Value' if value is None else f'value {value} {next_bit}'


def encoded(word, prefix, budget):
    value = encode_word(word)
    return dict(error='Value') if value is None else pack([(0, prefix), (value, 16)], budget)


def boundary_words():
    words = {0x80000000, 1, 0x80000001, 0x7f7fffff, 0xff7fffff, 0x477fe001, 0xc77fe001}
    for current, following in zip(HALVES, HALVES[1:]):
        low, high = float_bits(current), float_bits(following)
        for word in [low, low + 1, float_bits((current + following) / 2), high - 1]:
            words.update([word, word | 0x80000000])
    words.update([float_bits(HALVES[-1]), float_bits(-HALVES[-1])])
    for sign, payload in product([0, 0x80000000], [0, 1, 0x3fffff, 0x400000, 0x7fffff]):
        words.add(sign | 0x7f800000 | payload)
    return sorted(words)


def cases():
    # Boundary packets precede exhaustive batches so the small run exercises both directions.
    samples = [0, 1, 0x3ff, 0x400, 0x3c00, 0x7bff, 0x8000, 0x8001, 0xbc00, 0x7c00, 0xfc00, 0x7e00, 0xffff]
    writers = [0, 0x80000000, 1, 0x80000001, 0x33800000, 0xb3800000, 0x3f801001, 0x3f803000,
               0x477fe000, 0xc77fe000, 0x477fe001, 0x7f800000, 0xff800000, 0x7fc00000, 0x7f800001]
    for offset in range(8):
        data = pack([(0, offset), *[(word, 16) for word in samples]], 32)['data']
        queries = [(offset + i * 16, offset + i * 16 + cut) for i in range(len(samples)) for cut in range(17)]
        queries += [(-1, 16), (0, -1), (0, len(data) * 8 + 1), (2**63 - 1, len(data) * 8)]
        encodes = [(word, offset, 3) for word in writers]
        encodes += [(word, prefix, budget) for word, prefix, budget in product(writers[:4], [-1, 0, 7, 56, 57], [-1, 0, 1, 2, 3, 9])]
        yield dict(name=f'bounds-{offset}', data=data, queries=queries, encodes=encodes)
    for length, fill in product(range(4), [0, 255, 128, 37]):
        data = bytes([fill]) * length
        yield dict(name=f'physical-{length}-{fill}', data=data,
            queries=list(product(range(length * 8 + 1), range(length * 8 + 1))), encodes=[(0x3f800000, 0, 2)])
    for start in range(0, 65536, 256):
        offset = start // 256 % 8
        data = pack([(0, offset), *[(word, 16) for word in range(start, start + 256)]], 513)['data']
        yield dict(name=f'words-{start}', data=data, queries=[(offset + i * 16, offset + (i + 1) * 16) for i in range(256)],
            encodes=[(half_bits(word), offset, 3) for word in range(start, start + 256) if half_bits(word) is not None])
    candidates = boundary_words()
    for start in range(0, len(candidates), 256):
        yield dict(name=f'binary32-{start}', data=b'', queries=[],
            encodes=[(word, (start + i) % 8, 3) for i, word in enumerate(candidates[start:start + 256])])
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
            raise ValueError('real tone codestream profile differs')
        if [half_bits(encode_word(word)) for word in row['input_bits']] != row['output_bits']:
            raise ValueError('real tone basic information differs')
        queries = [(p, p + cut) for p in row['positions'] for cut in range(17)]
        yield dict(name=f'real-{index}', data=data, queries=queries,
            encodes=[(word, p % 8, 3) for word, p in zip(row['input_bits'], row['positions'], strict=True)])


def fixture(case):
    return (struct.pack('<III', len(case['queries']), len(case['encodes']), len(case['data']))
        + b''.join(struct.pack('<qq', *query) for query in case['queries'])
        + b''.join(struct.pack('<Iii', *query) for query in case['encodes']) + case['data'])


def check_worker(stdout, stderr, selected):
    wanted = []
    for index, case in enumerate(selected):
        wanted += [f'{index} query {i} ' + decode(case['data'], p, e) for i, (p, e) in enumerate(case['queries'])]
        for i, (word, prefix, budget) in enumerate(case['encodes']):
            result = encoded(word, prefix, budget)
            wanted.append(f'{index} encode {i} error {result["error"]}' if 'error' in result
                else f'{index} encode {i} ok {result["bits"]} {result["data"].hex()}')
    heaps = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
    if stdout.splitlines() != wanted or not heaps or not all(int(a) > 0 and a == b and c == '0' for a, b, c in heaps):
        raise ValueError('finite metadata results or heap evidence differ')


def reference_queries(case):
    words = []
    for p, _ in case['queries']:
        result = read(case['data'], p, 16, len(case['data']) * 8)
        if not result.startswith('error'):
            words.append(int(result.split()[1]))
    words = sorted(set(words))
    commands = ''.join(f'read {word}\n' for word in words)
    expected = ['value' if half_bits(word) is None else f'read {half_bits(word)} 16' for word in words]
    for word, _, _ in case['encodes']:
        commands += f'encode {word}\n'
        value = encode_word(word)
        expected.append('value' if value is None else f'encoded {value} 16')
    return commands.encode(), expected, len(words)


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
    sources = ['fern-image/packages/codecs/jxl/' + p + '.fern' for p in ['bits', 'f16_fields']]
    sources += ['fern-image/tests/' + p for p in ['jxl_f16_corpus.fern', 'reference_jxl_f16.cc', 'qualify_jxl_f16.py',
        'qualify_jxl_bits.py', 'fixtures/jxl_f16_sources.json', 'jxl_f16_fields_test.fern', 'qualify_jxl_f16_test.py',
        'fixtures/jxl_f16_reference.json', 'generate_jxl_f16.c', 'generate_jxl_f16.py',
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
    units = [('adapter', repo / 'fern-image/tests/reference_jxl_f16.cc', [])]
    units += [(name, reference / 'lib/jxl' / source, flags) for name, source, flags in [
        ('fields', 'fields.cc', ['-Wno-unused-parameter']), ('reader', 'dec_bit_reader.cc', []),
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
        repo / 'fern-image/tests/jxl_f16_corpus.fern', repo / 'internal/stdlib'], cwd=repo)
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
            commands, expected, reads = reference_queries(case)
            stdout, _ = run('reference-' + str(len(record['observations'])), [oracle], commands)
            if stdout.splitlines() != expected:
                raise ValueError('original finite coder results differ')
            record['observations'].append(dict(name=case['name'], input_sha256=digest(path), queries=len(case['queries']),
                reference_reads=reads, encodes=len(case['encodes'])))
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
