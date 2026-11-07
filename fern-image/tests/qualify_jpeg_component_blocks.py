"""Check bounded component blocks against explicit plane and edge semantics."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import struct
import subprocess
import sys
import tempfile


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def expected(case):
    width, height, stride, bx, by, edge, fill, data = case
    if width <= 0 or height <= 0 or stride < width:
        return 'error Layout'
    if (height - 1) * stride + width > len(data):
        return 'error Storage'
    if bx < 0 or by < 0 or bx * 8 >= width or by * 8 >= height:
        return 'error Bounds'
    plane = [data[y * stride:y * stride + width] for y in range(height)]
    output = []
    for y in range(by * 8, by * 8 + 8):
        for x in range(bx * 8, bx * 8 + 8):
            if edge and (x >= width or y >= height):
                output.append(fill)
            else:
                output.append(plane[min(y, height - 1)][min(x, width - 1)])
    return 'ok ' + bytes(output).hex()


def cases():
    for width in [1, 7, 8, 9, 17, 33, 257]:
        for height in [1, 7, 8, 9, 17]:
            for extra in [0, 3]:
                stride = width + extra
                data = bytes((x * 37 + y * 17 + 11) % 256 if x < width else 221 for y in range(height) for x in range(stride))
                last_x, last_y = (width - 1) // 8, (height - 1) // 8
                positions = sorted({(0, 0), (last_x, 0), (0, last_y), (last_x, last_y), (last_x // 2, last_y // 2)})
                for minimal in [False, True]:
                    payload = data[:(height - 1) * stride + width] if minimal else data
                    for bx, by in positions:
                        for edge, fill in [(0, 0), (1, 0), (1, 1), (1, 128), (1, 255)]:
                            yield width, height, stride, bx, by, edge, fill, payload
                yield width, height, stride, last_x, last_y, 0, 0, data[:(height - 1) * stride + width - 1]
    for fill in range(256):
        yield 1, 1, 1, 0, 0, 1, fill, bytes([17])
    for width, height, stride in [(0, 1, 1), (1, 0, 1), (-1, 1, 1), (1, -1, 1), (2, 1, 1), (1, 1, -1),
                                  (-2147483648, 1, 1), (2147483647, 2147483647, 2147483647), (1, 2147483647, 2147483647)]:
        yield width, height, stride, 0, 0, 0, 0, b''
    for bx, by in [(-1, 0), (0, -1), (1, 0), (0, 1), (2147483647, 2147483647), (-2147483648, -2147483648)]:
        yield 1, 1, 1, bx, by, 0, 0, bytes([17])
    yield 1, 1, 2147483647, 0, 0, 0, 0, bytes([17])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=Path)
    parser.add_argument('compiler', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--wasmtime', type=Path)
    parser.add_argument('--max-cases', type=int)
    args = parser.parse_args()
    if args.max_cases is not None and args.max_cases <= 0:
        parser.error('--max-cases must be positive')
    repo = Path(__file__).resolve().parents[2]
    paths = [Path(__file__), Path(__file__).with_name('jpeg_component_blocks.fern'),
             Path(__file__).with_name('jpeg_component_blocks_test.fern'), repo / 'fern-image/scripts/test-jpeg-component-blocks.sh',
             Path(sys.executable).resolve(), args.binary.resolve(), args.compiler.resolve()]
    paths += sorted((repo / 'fern-image/packages').rglob('*.fern'))
    paths += sorted((repo / 'internal/stdlib').rglob('*.fern'))
    paths += sorted((repo / 'internal/stdlib').rglob('*.fern.md'))
    if args.wasmtime:
        paths.append(args.wasmtime.resolve())
    frozen = {str(path): sha(path) for path in paths}
    inputs = list(cases())
    if args.max_cases is not None:
        inputs = inputs[:args.max_cases]
    runner = [str(args.wasmtime.resolve()), 'run', '--dir=.'] if args.wasmtime else []
    observations, batches = [], []
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        for start in range(0, len(inputs), 16):
            batch = inputs[start:start + 16]
            names = []
            for i, case in enumerate(batch):
                width, height, stride, bx, by, edge, fill, data = case
                path = root / f'block-{start + i}.raw'
                path.write_bytes(struct.pack('>5iBB', width, height, stride, bx, by, edge, fill) + data)
                names.append(path.name)
            result = subprocess.run([*runner, str(args.binary.resolve()), *names], cwd=root, capture_output=True, text=True, timeout=20, check=True)
            report = re.fullmatch(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?', result.stderr)
            if not report or int(report[1]) <= 0 or report[1] != report[2]:
                raise ValueError('Missing or unbalanced component heap census')
            lines = result.stdout.splitlines()
            if len(lines) != len(batch):
                raise ValueError('Component worker count differs')
            for path, line, case in zip(names, lines, batch):
                wanted = path + ' ' + expected(case)
                if line != wanted:
                    raise ValueError('Component reference differs: ' + line)
                data = struct.pack('>5iBB', *case[:7]) + case[7]
                if (root / path).read_bytes() != data:
                    raise ValueError('Component input changed')
                observations.append({'input_sha256': sha(root / path), 'observation': line[len(path) + 1:]})
            batches.append({'first': start, 'count': len(batch), 'exit_code': result.returncode, 'allocations': int(report[1])})
    if frozen != {str(path): sha(path) for path in paths}:
        raise ValueError('Component sources or tools changed')
    record = {'complete': True, 'max_cases': args.max_cases, 'files_sha256': frozen, 'samples': observations, 'batches': batches,
              'scope': 'Finite eight-bit component extraction with explicit replication or fill. '
                       'No public image encoder, quality default, speed or physical memory claim.'}
    args.output.write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps({'complete': True, 'cases': len(observations), 'batches': len(batches)}))


if __name__ == '__main__':
    main()
