"""Generate offline sample-precision fixtures with a pinned JPEG XL library."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import struct
import subprocess
import tempfile

from generate_jxl_headers import dependencies, digest
from qualify_jxl_headers import fields as dimension_fields
from qualify_jxl_bits import pack, read


def codestream(data):
    if data.startswith(b'\xff\x0a'):
        return data
    if not data.startswith(bytes.fromhex('0000000c4a584c200d0a870a')):
        raise ValueError('reference file has no JPEG XL signature')
    at, payloads = 0, []
    while at < len(data):
        if len(data) - at < 8:
            raise ValueError('truncated reference box')
        size, kind = struct.unpack_from('>I4s', data, at)
        header = 8
        if size == 1:
            if len(data) - at < 16:
                raise ValueError('truncated reference extended size')
            size, header = struct.unpack_from('>Q', data, at + 8)[0], 16
        elif size == 0:
            size = len(data) - at
        if size < header or size > len(data) - at:
            raise ValueError('invalid reference box extent')
        if kind == b'jxlc':
            payloads.append(data[at + header:at + size])
        at += size
    if len(payloads) != 1 or not payloads[0].startswith(b'\xff\x0a'):
        raise ValueError('fixture requires one complete codestream box')
    return payloads[0]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--reference', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    binary = args.reference.resolve()
    frozen = dependencies(binary)
    record = dict(generator_sha256=digest(Path(__file__)),
                  adapter_sha256=digest(Path(__file__).with_suffix('.c')),
                  reference_version='0.12.0',
                  reference_dependencies={('fixture-generator' if name == str(binary) else name): value
                                          for name, value in frozen.items()}, images=[])
    start = 16 + pack(dimension_fields(0, (1, 1, 0, 0))['fields'], 16)['bits']
    with tempfile.TemporaryDirectory(prefix='jxl-depth-fixtures-') as folder:
        for precision, exponent in [(1, 0), (8, 0), (10, 0), (12, 0), (16, 0), (16, 5), (24, 7), (32, 8)]:
            path = Path(folder) / 'fixture.jxl'
            result = subprocess.run([binary, str(precision), str(exponent), path], capture_output=True, check=True, timeout=60)
            file_bytes = path.read_bytes()
            data = codestream(file_bytes)
            if result.stdout.decode().split()[:2] != [str(precision), str(exponent)]:
                raise ValueError('reference basic information differs')
            if read(data, start, 2, len(data) * 8).split()[1] != '0':
                raise ValueError('fixture needs explicit precision and no extra metadata fields')
            record['images'].append(dict(bits=precision, exponent=exponent, position=start + 2,
                                         basic_info=result.stdout.decode().strip(), file_sha256=digest(path),
                                         file_base64=base64.b64encode(file_bytes).decode(),
                                         codestream_sha256=hashlib.sha256(data).hexdigest(),
                                         codestream_base64=base64.b64encode(data).decode()))
    if dependencies(binary) != frozen:
        raise ValueError('reference dependencies changed during fixture generation')
    with args.output.open('x') as stream:
        stream.write(json.dumps(record, indent=2) + '\n')
    print(json.dumps(dict(images=len(record['images']), dependencies=len(frozen))))


if __name__ == '__main__':
    main()
