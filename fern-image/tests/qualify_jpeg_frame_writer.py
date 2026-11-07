"""Check complete baseline frames against exact bytes and offline libjpeg coefficients."""

import argparse
import hashlib
import json
from pathlib import Path
import struct
import subprocess
import tempfile

from qualify_jpeg_entropy_writer import reference, table, heap


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def segment(marker, payload):
    return bytes([255, marker]) + struct.pack('>H', len(payload) + 2) + payload


def expected(case, width, height, quantizers):
    jfif = segment(224, b'JFIF\0' + bytes([1, 1, 0, 0, 1, 0, 1, 0, 0]))
    quant = segment(219, b'\0' + quantizers)
    frame = segment(192, struct.pack('>BHHBBBB', 8, height, width, 1, 1, 17, 0))
    huffman = b''.join(segment(196, bytes([kind]) + bytes(case[name]['counts']) + bytes(case[name]['symbols']))
                       for kind, name in [(0, 'dc'), (16, 'ac')])
    restart = segment(221, struct.pack('>H', case['restart'])) if case['restart'] else b''
    return b'\xff\xd8' + jfif + quant + frame + huffman + restart + segment(218, bytes([1, 1, 0, 0, 63, 0])) + reference(case) + b'\xff\xd9'


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
    paths = [Path(__file__), Path(__file__).with_name('qualify_jpeg_entropy_writer.py'),
             Path(__file__).with_name('reference_jpeg_coefficients.c'),
             repo / 'fern-image/scripts/test-jpeg-frame-writer.sh',
             Path(__file__).with_name('jpeg_frame_writer.fern'), args.binary.resolve(),
             args.compiler.resolve(), args.oracle.resolve(), args.reference_library.resolve()]
    paths += sorted((repo / 'fern-image/packages').rglob('*.fern'))
    paths += sorted((repo / 'internal/stdlib').rglob('*.fern'))
    paths += sorted((repo / 'internal/stdlib').rglob('*.fern.md'))
    if args.wasmtime:
        paths.append(args.wasmtime.resolve())
    frozen = {str(path): sha(path) for path in paths}
    cases = []
    for width in [1, 8, 9, 17, 257]:
        for height in [1, 8, 9, 25]:
            count = ((width + 7) // 8) * ((height + 7) // 8) * 64
            values = [i // 64 * 3 - 20 if i % 64 == 0 else i % 33 - 16 if i % 7 == 0 else 0 for i in range(count)]
            for interval in [0, 1, 2, 7, 65535]:
                case = {'values': values, 'dc': table(False), 'ac': table(True), 'restart': interval, 'limit': 65536}
                quantizers = bytes((i * 37 + len(cases) * 13) % 255 + 1 for i in range(64))
                cases.append((width, height, quantizers, case))
    if args.max_cases is not None:
        cases = cases[:args.max_cases]
    observations, batches = [], []
    runner = [str(args.wasmtime.resolve()), 'run', '--dir=.'] if args.wasmtime else []
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        for start in range(0, len(cases), 8):
            batch = cases[start:start + 8]
            names = []
            for i, (width, height, quantizers, case) in enumerate(batch):
                data = struct.pack('>iiii', width, height, case['restart'], case['limit']) + quantizers
                for name in ['dc', 'ac']:
                    data += bytes(case[name]['counts']) + struct.pack('>H', len(case[name]['symbols'])) + bytes(case[name]['symbols'])
                data += b''.join(struct.pack('>i', value) for value in case['values'])
                name = f'case-{start + i}.bin'
                (root / name).write_bytes(data)
                names.append(name)
            run = subprocess.run([*runner, str(args.binary.resolve()), *names], cwd=root, capture_output=True, text=True, timeout=30, check=True)
            heap(run.stderr)
            lines = run.stdout.splitlines()
            if len(lines) != len(batch):
                raise ValueError('Frame worker output count differs')
            for name, line, (width, height, quantizers, case) in zip(names, lines, batch):
                prefix = name + ' ok '
                if not line.startswith(prefix):
                    raise ValueError('Frame writer rejected a valid case')
                encoded = bytes.fromhex(line[len(prefix):])
                if encoded != expected(case, width, height, quantizers):
                    raise ValueError('Complete frame bytes differ')
                image = root / 'frame.jpg'
                image.write_bytes(encoded)
                oracle = subprocess.run([str(args.oracle.resolve()), str(image)], capture_output=True, text=True, timeout=30, check=True)
                if oracle.stderr:
                    raise ValueError(oracle.stderr)
                actual = json.loads(oracle.stdout)
                blocks = [case['values'][i:i + 64] for i in range(0, len(case['values']), 64)]
                if actual['width'] != width or actual['height'] != height or len(actual['components']) != 1 or actual['components'][0]['blocks'] != blocks:
                    raise ValueError('Independent frame coefficients differ')
                observations.append({'width': width, 'height': height, 'restart': case['restart'],
                                     'jpeg_sha256': sha(image), 'input_sha256': sha(root / name),
                                     'oracle_stdout_sha256': hashlib.sha256(oracle.stdout.encode()).hexdigest(),
                                     'reference': actual['reference']})
            batches.append({'first': start, 'cases': len(batch), 'exit_code': run.returncode,
                            'stderr_sha256': hashlib.sha256(run.stderr.encode()).hexdigest()})
    if frozen != {str(path): sha(path) for path in paths}:
        raise ValueError('Frame sources or tools changed')
    record = {'complete': True, 'max_cases': args.max_cases, 'files_sha256': frozen,
              'samples': observations, 'batches': batches,
              'scope': 'Finite one-component baseline frames from quantized blocks. Exact Python bytes and offline libjpeg coefficients. '
                       'No public pixel encoder, broad JPEG, speed or physical memory claim.'}
    args.output.write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps({'complete': True, 'cases': len(observations), 'batches': len(batches)}))


if __name__ == '__main__':
    main()
