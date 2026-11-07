"""Check component folds and complete frames with independent numeric and JPEG references."""

import argparse
import decimal
import hashlib
import json
import math
from pathlib import Path
import random
import struct
import subprocess
import sys
import tempfile

from generate_jpeg_idct import basis
from qualify_jpeg_entropy_writer import heap, permutation, table
from qualify_jpeg_frame_writer import expected as frame_bytes


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def transform(samples, matrix):
    if len(set(samples)) == 1:
        return [(samples[0] - 128) * 8.0] + [0.0] * 63
    rows = []
    for y in range(8):
        row = []
        for u in range(8):
            total = 0.0
            for x in range(8):
                total += (samples[y * 8 + x] - 128) * matrix[x][u]
            row.append(total)
        rows.append(row)
    coefficients = []
    for v in range(8):
        for u in range(8):
            total = 0.0
            for y in range(8):
                total += rows[y][u] * matrix[y][v]
            coefficients.append(total)
    coefficients[0] = sum(value - 128 for value in samples) / 8.0
    return coefficients


def expected(case, matrix):
    width, height, stride, limit, edge, fill, q, data = case
    if width <= 0 or height <= 0 or stride < width:
        return 'Layout'
    if (height - 1) * stride + width > len(data):
        return 'Storage'
    if len(q) != 64 or 0 in q:
        return 'Quantizer'
    columns, rows = (width + 7) // 8, (height + 7) // 8
    if limit < 0 or columns * rows > limit:
        return 'Limit'
    quantizers = [0] * 64
    for index, natural in enumerate(permutation()):
        quantizers[natural] = q[index]
    result = []
    for by in range(rows):
        for bx in range(columns):
            samples = []
            for y in range(by * 8, by * 8 + 8):
                for x in range(bx * 8, bx * 8 + 8):
                    samples.append(fill if edge and (x >= width or y >= height)
                                   else data[min(y, height - 1) * stride + min(x, width - 1)])
            for value, divisor in zip(transform(samples, matrix), quantizers, strict=True):
                quotient = value / divisor
                integer = math.floor(abs(quotient))
                rounded = integer + int(abs(quotient) - integer >= 0.5)
                result.append(-rounded if quotient < 0 else rounded)
    return result


def cases():
    control = bytes((i * 37 + 11) % 256 for i in range(108))
    yield 9, 9, 12, 4, 0, 0, bytes([1] * 64), control
    yield 9, 9, 12, 4, 1, 128, bytes(range(1, 65)), control
    yield 9, 9, 12, 3, 0, 0, bytes([1] * 64), control
    for value in range(256):
        yield 1, 1, 2147483647, 1, 0, 0, bytes([value % 255 + 1] * 64), bytes([value])
    rng = random.Random(20261107)
    for width in [1, 7, 8, 9, 17, 33]:
        for height in [1, 8, 9, 17]:
            for extra in [0, 3]:
                stride = width + extra
                count = ((width + 7) // 8) * ((height + 7) // 8)
                data = bytes(rng.randrange(256) if x < width else 77
                             for y in range(height) for x in range(stride))
                data = data[:(height - 1) * stride + width]
                for edge, fill in [(0, 0), (1, 0), (1, 128), (1, 255)]:
                    for q in [bytes([1] * 64), bytes((i * 37 + width + height) % 255 + 1 for i in range(64))]:
                        yield width, height, stride, count, edge, fill, q, data
                yield width, height, stride, count - 1, 0, 0, bytes([1] * 64), data
                yield width, height, stride, count, 0, 0, bytes([1] * 64), data[:-1]
    for fill in range(256):
        yield 1, 1, 1, 1, 1, fill, bytes([7] * 64), bytes([17])
    for index in range(64):
        data = bytearray([128] * 64)
        data[index] = 255
        yield 8, 8, 8, 1, 0, 0, bytes(range(1, 65)), bytes(data)
    for q in [b'', bytes([1] * 63), bytes([1] * 65), bytes([1] * 63 + [0])]:
        yield 1, 1, 1, 1, 0, 0, q, bytes([17])
    for limit in [-2147483648, -1, 0]:
        yield 1, 1, 1, limit, 0, 0, bytes([1] * 64), bytes([17])
    for width, height, stride in [(0, 1, 1), (1, 0, 1), (-1, 1, 1), (2, 1, 1), (1, 1, -1),
                                  (2147483647, 2147483647, 2147483647), (1, 2147483647, 2147483647)]:
        yield width, height, stride, 2147483647, 0, 0, bytes([1] * 64), b''


def payload(case):
    width, height, stride, limit, edge, fill, q, data = case
    return struct.pack('>4iBBH', width, height, stride, limit, edge, fill, len(q)) + q + data


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=Path)
    parser.add_argument('compiler', type=Path)
    parser.add_argument('oracle', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--reference-library', type=Path, required=True)
    parser.add_argument('--wasmtime', type=Path)
    parser.add_argument('--max-cases', type=int)
    args = parser.parse_args()
    if args.max_cases is not None and args.max_cases <= 0:
        parser.error('--max-cases must be positive')
    repo = Path(__file__).resolve().parents[2]
    paths = [Path(__file__), Path(__file__).with_name('generate_jpeg_idct.py'),
             Path(__file__).with_name('qualify_jpeg_entropy_writer.py'),
             Path(__file__).with_name('qualify_jpeg_frame_writer.py'),
             Path(__file__).with_name('reference_jpeg_coefficients.c'),
             Path(__file__).with_name('jpeg_plane_coefficients.fern'),
             Path(__file__).with_name('jpeg_plane_coefficients_test.fern'),
             repo / 'fern-image/scripts/test-jpeg-plane-coefficients.sh',
             Path(sys.executable).resolve(), args.binary.resolve(), args.compiler.resolve(),
             args.oracle.resolve(), args.reference_library.resolve()]
    paths += sorted((repo / 'fern-image/packages').rglob('*.fern'))
    paths += sorted((repo / 'internal/stdlib').rglob('*.fern'))
    paths += sorted((repo / 'internal/stdlib').rglob('*.fern.md'))
    if args.wasmtime:
        paths.append(args.wasmtime.resolve())
    frozen = {str(path): sha(path) for path in paths}
    with decimal.localcontext() as context:
        context.prec = 90
        matrix = [[float(value) for value in row] for row in basis()]
    inputs = list(cases())
    if args.max_cases is not None:
        inputs = inputs[:args.max_cases]
    observations, batches = [], []
    runner = [str(args.wasmtime.resolve()), 'run', '--dir=.'] if args.wasmtime else []
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        for first in range(0, len(inputs), 8):
            batch = inputs[first:first + 8]
            names = []
            for index, case in enumerate(batch):
                name = f'plane-{first + index}.raw'
                (root / name).write_bytes(payload(case))
                names.append(name)
            run = subprocess.run([*runner, str(args.binary.resolve()), *names], cwd=root,
                                 capture_output=True, text=True, timeout=30, check=True)
            heap(run.stderr)
            lines = run.stdout.splitlines()
            if len(lines) != len(batch):
                raise ValueError('Plane worker output count differs')
            for name, case, line in zip(names, batch, lines, strict=True):
                wanted = expected(case, matrix)
                fields = line.split()
                if isinstance(wanted, str):
                    if line != name + ' error ' + wanted:
                        raise ValueError('Plane error differs: ' + line)
                    observation = {'error': wanted}
                else:
                    if len(fields) != len(wanted) + 3 or fields[:2] != [name, 'ok']:
                        raise ValueError('Plane worker shape differs')
                    if [int(value) for value in fields[2:-1]] != wanted:
                        raise ValueError('Complete quantized plane differs: ' + name)
                    encoded = bytes.fromhex(fields[-1])
                    frame = {'values': wanted, 'dc': table(False), 'ac': table(True), 'restart': 2, 'limit': 1048576}
                    if encoded != frame_bytes(frame, case[0], case[1], case[6]):
                        raise ValueError('Complete plane frame bytes differ')
                    image = root / 'plane.jpg'
                    image.write_bytes(encoded)
                    oracle = subprocess.run([str(args.oracle.resolve()), str(image)], capture_output=True,
                                            text=True, timeout=30, check=True)
                    if oracle.stderr:
                        raise ValueError(oracle.stderr)
                    decoded = json.loads(oracle.stdout)
                    blocks = [wanted[i:i + 64] for i in range(0, len(wanted), 64)]
                    if (decoded['width'], decoded['height']) != case[:2] or len(decoded['components']) != 1 or decoded['components'][0]['blocks'] != blocks:
                        raise ValueError('Independent JPEG coefficient decode differs')
                    observation = {'coefficient_sha256': hashlib.sha256(json.dumps(wanted).encode()).hexdigest(),
                                   'jpeg_sha256': sha(image), 'reference': decoded['reference']}
                if (root / name).read_bytes() != payload(case):
                    raise ValueError('Plane input changed')
                observations.append({'input_sha256': sha(root / name), 'observation': observation})
            batches.append({'first': first, 'cases': len(batch), 'exit_code': run.returncode,
                            'stderr_sha256': hashlib.sha256(run.stderr.encode()).hexdigest()})
    if frozen != {str(path): sha(path) for path in paths}:
        raise ValueError('Plane sources or tools changed')
    record = {'complete': True, 'max_cases': args.max_cases, 'files_sha256': frozen,
              'samples': observations, 'batches': batches,
              'scope': 'Finite explicit component folds and complete frames. Exact binary64 separable reference, '
                       'independently derived basis and offline libjpeg coefficient decode. '
                       'No public image policy, complete JPEG, speed or physical memory claim.'}
    args.output.write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps({'complete': True, 'cases': len(observations), 'batches': len(batches)}))


if __name__ == '__main__':
    main()
