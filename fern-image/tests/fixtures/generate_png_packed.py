"""Verify packed grayscale fixtures with independent PNG equations and Pillow."""

import argparse
import io
from pathlib import Path
import struct
import zlib

from PIL import Image


def chunk(kind, data):
    return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))


def vector(depth, width, padding):
    maximum = (1 << depth) - 1
    rows, expected = [], bytearray()
    for y in range(5):
        samples = [(x + y * 3) % (maximum + 1) for x in range(width)]
        bits = ''.join(format(sample, f'0{depth}b') for sample in samples)
        bits += str(padding) * (-len(bits) % 8)
        rows.append(bytes(int(bits[i:i + 8], 2) for i in range(0, len(bits), 8)))
        for sample in samples:
            expected.extend([sample * 255 // maximum] * 3)
    filtered = bytearray()
    for y, row in enumerate(rows):
        filtered.append(y)
        for x, value in enumerate(row):
            left = row[x - 1] if x else 0
            above = rows[y - 1][x] if y else 0
            corner = rows[y - 1][x - 1] if y and x else 0
            p = left + above - corner
            candidates = [left, above, corner]
            paeth = min(enumerate(candidates), key=lambda item: (abs(p - item[1]), item[0]))[1]
            prediction = [0, left, above, (left + above) // 2, paeth][y]
            filtered.append((value - prediction) % 256)
    header = struct.pack('>IIBBBBB', width, 5, depth, 0, 0, 0, 0)
    data = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', header) + chunk(b'IDAT', zlib.compress(filtered)) + chunk(b'IEND', b'')
    with Image.open(io.BytesIO(data)) as image:
        assert image.size == (width, 5)
        assert image.convert('RGB').tobytes() == expected
    return data, bytes(expected)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    options = parser.parse_args()
    rows = []
    for depth in [1, 2, 4]:
        for width in [1, 7, 8, 9, 17, 33, 129]:
            for padding in [0, 1]:
                data, expected = vector(depth, width, padding)
                rows.append((data, expected, width, depth))
    lines = ['import "std/hex";', '', 'pub struct Case { encoded: u8[], expected: u8[], width: i32, depth: i32 }', '',
             'pub function count(): i32 {', f'  return {len(rows)};', '}', '', 'pub function get(index: i32): Case {',
             '  let (data, pixels, width, depth) = match (index) {']
    for index, (data, expected, width, depth) in enumerate(rows):
        lines.append(f'    {index} => ("{data.hex()}", "{expected.hex()}", {width}, {depth}),')
    lines.extend(['    _ => ("", "", 0, 0),', '  };',
                  '  let Ok(encoded) = hex.decode(data) else {',
                  '    return Case { encoded: [], expected: [], width: 0, depth: 0 };', '  };',
                  '  let Ok(expected) = hex.decode(pixels) else {',
                  '    return Case { encoded: [], expected: [], width: 0, depth: 0 };', '  };',
                  '  return Case { encoded: encoded, expected: expected, width: width, depth: depth };', '}', ''])
    source = '\n'.join(lines)
    if options.check:
        assert Path(__file__).with_name('png_packed.fern').read_text() == source
    else:
        print(source, end='')


if __name__ == '__main__':
    main()
