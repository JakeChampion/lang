"""Generate offline tone metadata fixtures with a pinned JPEG XL library."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile

from generate_jxl_headers import dependencies, digest
from generate_jxl_sample_depth import codestream
from qualify_jxl_f16 import encode_word, float_bits, half_bits
from qualify_jxl_headers import fields as dimension_fields
from qualify_jxl_bits import pack, read


def positions(data):
    # This closed fixture profile is a 1x1 RGB8 image with default sRGB encoding.
    size = pack(dimension_fields(0, (1, 1, 0, 0))['fields'], 16)
    if size['bits'] != 15:
        raise ValueError('reference dimension profile differs')
    expected = [(16, 15, int.from_bytes(size['data'], 'little')), (31, 2, 2), (33, 6, 0), (39, 3, 0), (42, 1, 1),
                (43, 2, 0), (45, 1, 0), (46, 1, 1), (47, 1, 0), (80, 1, 0)]
    for at, width, value in expected:
        result = read(data, at, width, len(data) * 8)
        if result != f'ok {value} {at + width}':
            raise ValueError('reference tone fixture profile differs')
    return [48, 64, 81]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--reference', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    binary = args.reference.resolve()
    frozen = dependencies(binary)
    record = dict(generator_sha256=digest(Path(__file__)), adapter_sha256=digest(Path(__file__).with_suffix('.c')),
        reference_version='0.12.0', reference_dependencies={('fixture-generator' if name == str(binary) else name): value
            for name, value in frozen.items()}, images=[])
    with tempfile.TemporaryDirectory(prefix='jxl-f16-fixtures-') as folder:
        for samples in [(1000.0, 0.0, 0.0), (255.0, 0.00006, 0.5), (65504.0, 1.0, 10.0),
                        (1.0, 0.00000006, 0.00000006), (1000.25, 0.12345, 0.75)]:
            inputs = list(map(float_bits, samples))
            expected = [half_bits(encode_word(word)) for word in inputs]
            path = Path(folder) / 'fixture.jxl'
            result = subprocess.run([str(binary), *map(str, inputs), str(path)], capture_output=True, check=True, timeout=60)
            actual = list(map(int, result.stdout.decode().split()))
            if actual != expected:
                raise ValueError('reference tone basic information differs')
            original = path.read_bytes()
            data = codestream(original)
            offsets = positions(data)
            for at, value in zip(offsets, inputs, strict=True):
                if read(data, at, 16, len(data) * 8) != f'ok {encode_word(value)} {at + 16}':
                    raise ValueError('reference tone bytes differ')
            record['images'].append(dict(input_bits=inputs, output_bits=actual, positions=offsets,
                file_sha256=digest(path), file_base64=base64.b64encode(original).decode(),
                codestream_sha256=hashlib.sha256(data).hexdigest(), codestream_base64=base64.b64encode(data).decode()))
    if dependencies(binary) != frozen:
        raise ValueError('reference dependencies changed during fixture generation')
    with args.output.open('x') as stream:
        stream.write(json.dumps(record, indent=2) + '\n')
    print(json.dumps(dict(images=len(record['images']), dependencies=len(frozen))))


if __name__ == '__main__':
    main()
