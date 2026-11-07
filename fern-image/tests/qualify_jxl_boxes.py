"""Check Fern box envelopes with byte equations and offline libjxl 0.12.0."""

import argparse
import ctypes
import hashlib
from itertools import islice, permutations
import json
import os
from pathlib import Path
import re
import struct
import subprocess

from PIL import Image
from PIL import __version__ as pillow_version

SIGNATURE = bytes.fromhex('0000000c4a584c200d0a870a')
MAX_INPUT = 1048576
MAX_BOXES = 4096


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def envelope(kind, payload, form='Short'):
    if form == 'Short':
        return struct.pack('>I4s', len(payload) + 8, kind) + payload
    if form == 'Extended':
        return struct.pack('>I4sQ', 1, kind, len(payload) + 16) + payload
    if form == 'ToEnd':
        return struct.pack('>I4s', 0, kind) + payload
    raise ValueError('unknown box form')


def inspect(data):
    if len(data) > MAX_INPUT:
        return {'error': 'Limit'}
    headers = []
    offset = 0
    while offset < len(data):
        if len(headers) == MAX_BOXES:
            return {'error': 'Limit'}
        if len(data) - offset < 8:
            return {'error': 'Truncated'}
        length, kind = struct.unpack_from('>II', data, offset)
        header, form = 8, 'Short'
        if length == 1:
            if len(data) - offset < 16:
                return {'error': 'Truncated'}
            length, = struct.unpack_from('>Q', data, offset + 8)
            header, form = 16, 'Extended'
        elif length == 0:
            length, form = len(data) - offset, 'ToEnd'
        if length < header:
            return {'error': 'Length'}
        if offset + length > len(data):
            return {'error': 'Truncated'}
        headers.append((kind, form, offset, offset + header, length - header, offset + length))
        offset += length
    return {'headers': headers}


def cases(raw, boxed):
    yield dict(name='reference-container', data=boxed, container=True)
    for ftyp_form in ['Short', 'Extended']:
        ftyp = envelope(b'ftyp', b'jxl ' + struct.pack('>I', 0) + b'jxl ', ftyp_form)
        for stream_form in ['Short', 'Extended', 'ToEnd']:
            for count in [0, 3, 257]:
                metadata = envelope(b'test', bytes((i * 37) % 256 for i in range(count)), ftyp_form)
                yield dict(name=f'container-{ftyp_form}-{stream_form}-{count}',
                           data=SIGNATURE + ftyp + metadata + envelope(b'jxlc', raw, stream_form), container=True)
    parts = [raw[:len(raw) // 3], raw[len(raw) // 3:2 * len(raw) // 3], raw[2 * len(raw) // 3:]]
    for order in permutations(range(3)):
        ftyp = envelope(b'ftyp', b'jxl ' + struct.pack('>I', 1) + b'jxl ')
        fragments = b''.join(envelope(b'jxlp', struct.pack('>I', i | (0x80000000 if i == 2 else 0)) + parts[i], 'Extended') for i in order)
        yield dict(name='partial-v1-' + ''.join(map(str, order)), data=SIGNATURE + ftyp + fragments, container=True)
    yield dict(name='raw-codestream-signature', data=raw)
    for form in ['Short', 'Extended', 'ToEnd']:
        for kind in [b'\0' * 4, b'\xff' * 4, b'jxlc', b'brob', b'uuid']:
            for count in [0, 1, 7, 8, 15, 16, 255, 256, 257, 4096, 65536]:
                yield dict(name=f'envelope-{form}-{kind.hex()}-{count}', data=envelope(kind, bytes((i * 37) % 256 for i in range(count)), form))
    for count in [0, 1, 2, 7, 31, 257, MAX_BOXES, MAX_BOXES + 1]:
        yield dict(name=f'box-count-{count}', data=envelope(b'test', b'') * count)
    for first in range(256):
        yield dict(name=f'first-byte-{first}', data=bytes([first]))
    for cut in range(len(SIGNATURE) + 1):
        yield dict(name=f'signature-prefix-{cut}', data=SIGNATURE[:cut])
    for i in range(len(SIGNATURE)):
        value = bytearray(SIGNATURE)
        value[i] ^= 1
        yield dict(name=f'signature-bit-{i}', data=bytes(value))
    for length in range(2, 8):
        yield dict(name=f'short-length-{length}', data=struct.pack('>I4s', length, b'test'))
    for length in [0, 1, 7, 8, 15, 17, 0x80000000, 0xffffffff, 0x100000000, 0x8000000000000000, 0xffffffffffffffff]:
        yield dict(name=f'extended-length-{length}', data=struct.pack('>I4sQ', 1, b'test', length))
    for form in ['Short', 'Extended', 'ToEnd']:
        value = envelope(b'test', bytes(range(17)), form)
        for cut in range(len(value)):
            yield dict(name=f'prefix-{form}-{cut}', data=value[:cut])
    yield dict(name='input-byte-budget', data=b'\0' * (MAX_INPUT + 1))


def check_worker(stdout, stderr, selected, signatures):
    lines = stdout.splitlines()
    census = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
    if len(lines) != len(selected) or not census or not all(int(a) > 0 and a == b and c == '0' for a, b, c in census):
        raise ValueError('worker results or heap evidence differ')
    for index, (line, case, signature) in enumerate(zip(lines, selected, signatures, strict=True)):
        expected = inspect(case['data'])
        fields = line.split(' ', 4)
        if fields[:2] != [str(index), str(signature)]:
            raise ValueError('worker signature or index differs')
        if 'error' in expected:
            if fields[2:] != ['error', expected['error']]:
                raise ValueError('worker framing error differs')
        else:
            description = ''.join(':'.join(map(str, header)) + ',' for header in expected['headers'])
            if fields[2:] != ['ok', str(len(expected['headers'])), description]:
                raise ValueError('worker box descriptor differs')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fern', type=Path, required=True)
    parser.add_argument('--target', choices=['arm64-darwin', 'arm64-linux', 'x86-64-linux', 'wasm32-wasi'], required=True)
    parser.add_argument('--runner', action='append', default=[])
    parser.add_argument('--reference-library', type=Path, required=True)
    parser.add_argument('--reference-encoder', type=Path, required=True)
    parser.add_argument('--reference-decoder', type=Path, required=True)
    parser.add_argument('--reference-boxes', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--max-cases', type=int)
    args = parser.parse_args()
    if args.max_cases is not None and args.max_cases <= 0:
        parser.error('max-cases must be positive')
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    repo = Path(__file__).resolve().parents[2]
    compiler = args.fern.resolve()
    library = ctypes.CDLL(str(args.reference_library.resolve()))
    library.JxlDecoderVersion.restype = ctypes.c_uint32
    if library.JxlDecoderVersion() != 12000:
        raise ValueError('reference library must be libjxl 0.12.0')
    library.JxlSignatureCheck.argtypes = [ctypes.c_void_p, ctypes.c_size_t]
    library.JxlSignatureCheck.restype = ctypes.c_int
    sources = [repo / 'fern-image/packages/codecs/jxl/boxes.fern', repo / 'fern-image/tests/jxl_boxes_corpus.fern',
               repo / 'fern-image/tests/reference_jxl_boxes.c', Path(__file__).resolve()]
    tools = dict(compiler=compiler, reference_library=args.reference_library.resolve(), reference_encoder=args.reference_encoder.resolve(),
                 reference_decoder=args.reference_decoder.resolve(), reference_boxes=args.reference_boxes.resolve())
    frozen = {str(p.relative_to(repo)): digest(p) for p in sources}
    tool_hashes = {name: digest(p) for name, p in tools.items()}
    record = dict(complete=False, target=args.target, source_inventory=frozen, tool_sha256=tool_hashes,
                  reference_library_version=library.JxlDecoderVersion(), pillow=pillow_version,
                  limits=dict(max_input_bytes=MAX_INPUT, max_boxes=MAX_BOXES), commands=[], observations=[])
    env = dict(os.environ, FERN_LEAKCHECK='1')

    def run(label, command, cwd=out):
        result = subprocess.run(list(map(str, command)), cwd=cwd, env=env, capture_output=True, timeout=120)
        stdout, stderr = out / (label + '.stdout'), out / (label + '.stderr')
        stdout.write_bytes(result.stdout)
        stderr.write_bytes(result.stderr)
        record['commands'].append(dict(label=label, command=list(map(str, command)), exit_code=result.returncode,
                                       stdout_sha256=digest(stdout), stderr_sha256=digest(stderr)))
        (out / 'proof.json').write_text(json.dumps(record, indent=2) + '\n')
        result.check_returncode()
        return result

    for name in ['reference_encoder', 'reference_decoder']:
        run(name + '-version', [tools[name], '--version'])
    ppm = out / 'source.ppm'
    ppm.write_bytes(b'P6\n1 1\n255\n' + bytes([37, 83, 149]))
    for form in [0, 1]:
        run('reference-encode-' + str(form), [tools['reference_encoder'], ppm, out / f'source-{form}.jxl', '-d', '0', '-e', '1', '--num_threads=1', f'--container={form}'])
    values = list(islice(cases((out / 'source-0.jxl').read_bytes(), (out / 'source-1.jxl').read_bytes()), args.max_cases))
    binary = out / ('worker.wasm' if args.target == 'wasm32-wasi' else 'worker')
    run('build', [compiler, '-target', args.target, '-sanitize', '-o', binary, repo / 'fern-image/tests/jxl_boxes_corpus.fern', repo / 'internal/stdlib'], cwd=repo)
    record['binary_sha256'] = digest(binary)
    for first in range(0, len(values), 16):
        batch = values[first:first + 16]
        paths, signatures = [], []
        for index, case in enumerate(batch, first):
            path = out / f'input-{index}.bin'
            path.write_bytes(case['data'])
            paths.append(path.name)
            signatures.append(library.JxlSignatureCheck(ctypes.c_char_p(case['data']), len(case['data'])))
        result = run('batch-' + str(first), [*args.runner, binary, *paths])
        check_worker(result.stdout.decode(), result.stderr.decode(), batch, signatures)
        for index, case in enumerate(batch, first):
            path = out / f'input-{index}.bin'
            expected = inspect(case['data'])
            row = dict(name=case['name'], input_sha256=digest(path), signature=signatures[index - first],
                       framing=expected, reference_container=case.get('container', False))
            if 'headers' in expected:
                rewritten = path.with_name(path.name + '.roundtrip')
                if rewritten.read_bytes() != case['data']:
                    raise ValueError('box round trip changes complete bytes')
                row['roundtrip_sha256'] = digest(rewritten)
            if case.get('container'):
                trace = run('boxes-' + str(index), [tools['reference_boxes'], path]).stdout.decode().splitlines()
                wanted = []
                for kind, form, start, at, count, end in expected['headers']:
                    wanted.append(f'{kind:08x} {0 if form == "ToEnd" else end - start} {case["data"][at:end].hex()}')
                if trace != wanted:
                    raise ValueError('libjxl box trace differs')
                png = out / f'decoded-{index}.png'
                run('decode-' + str(index), [tools['reference_decoder'], path.with_name(path.name + '.roundtrip'), png, '--num_threads=1'])
                with Image.open(png) as image:
                    if image.size != (1, 1) or image.convert('RGB').tobytes() != bytes([37, 83, 149]):
                        raise ValueError('rewrapped reference pixels differ')
                row['reference_pixels_sha256'] = hashlib.sha256(bytes([37, 83, 149])).hexdigest()
            record['observations'].append(row)
    if frozen != {str(p.relative_to(repo)): digest(p) for p in sources} or tool_hashes != {name: digest(p) for name, p in tools.items()}:
        raise ValueError('sources or tools changed during qualification')
    record.update(complete=True, cases=len(values))
    (out / 'proof.json').write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps(dict(complete=True, cases=len(values), containers=sum(c.get('container', False) for c in values), target=args.target)))


if __name__ == '__main__':
    main()
