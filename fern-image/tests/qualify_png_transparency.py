"""Check canonical transparency pixels against an offline libpng executable."""

import argparse
import hashlib
import json
from pathlib import Path
import platform
import subprocess
import tempfile

from fixtures.generate_png_transparency import vector


def sha(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('reference', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--reference-library', type=Path, required=True)
    parser.add_argument('--max-cases', type=int)
    options = parser.parse_args()
    if options.max_cases is not None and options.max_cases <= 0:
        parser.error('--max-cases must be positive')
    reference = options.reference.resolve()
    root = Path(__file__).resolve().parents[1]
    paths = {'binary': reference, 'collector': Path(__file__),
             'reference_library': options.reference_library.resolve(),
             'generator': root / 'tests/fixtures/generate_png_transparency.py',
             'reference_source': root / 'tests/png_reference.c'}
    hashes = {name: sha(path.read_bytes()) for name, path in paths.items()}
    samples = []
    with tempfile.TemporaryDirectory() as temporary:
        path = Path(temporary) / 'case.png'
        for depth, color in [(1, 0), (2, 0), (4, 0), (8, 0), (8, 2)]:
            keys = range(1 << depth) if depth <= 4 else [0, 1, 7, 63, 127, 128, 254, 255]
            for key in keys:
                for width in [1, 7, 8, 9, 17, 129, 257, 8193]:
                    for padding in [0, 1]:
                        if options.max_cases is not None and len(samples) >= options.max_cases:
                            continue
                        _, expected, normalized = vector(depth, color, width, key, padding)
                        path.write_bytes(normalized)
                        result = subprocess.run([str(reference), str(path)], capture_output=True, timeout=20)
                        if result.returncode != 0 or result.stdout != expected:
                            raise ValueError(f'libpng mismatch: color={color}, depth={depth}, width={width}, key={key}, pad={padding}')
                        samples.append({'color_type': color, 'depth': depth, 'width': width, 'height': 5,
                                        'key': key, 'padding': padding, 'input_sha256': sha(normalized),
                                        'rgba_sha256': sha(expected), 'exit_code': result.returncode})
    if hashes != {name: sha(path.read_bytes()) for name, path in paths.items()}:
        raise ValueError('Reference sources changed')
    record = {'complete': True, 'host': platform.platform(), 'sha256': hashes,
              'paths': {name: str(path.resolve()) for name, path in paths.items()},
              'reference_version': subprocess.check_output([str(reference), '--version'], text=True).strip(),
              'max_cases': options.max_cases, 'samples': samples,
              'scope': 'Canonical keys, exact RGB and alpha, all five filters, packed padding and reader boundaries. '
                       'Unused key bits use normalized canonical reference inputs under PNG third edition rules. '
                       'Pillow checks gray samples with its transparency key at the expanded depth. '
                       'The Fern corpus checks original and nonzero unused key bits. No runtime codec dependency.'}
    options.output.write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps({'complete': True, 'cases': len(samples), 'libpng': record['reference_version']}))


if __name__ == '__main__':
    main()
