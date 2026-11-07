"""Create indexed PNG fixtures with independent filters and Pillow checks."""

import argparse
import io
from pathlib import Path
import struct
import zlib

from PIL import Image


def chunk(kind, data):
    return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))


def vector(depth, width, padding, alpha_count, entries=None, bad_index=False, height=5):
    entries = entries or (1 << depth)
    colors = bytes(value for i in range(entries) for value in (i, 255 - i, i * 37 % 256))
    alpha = bytes(i * 71 % 256 for i in range(max(0, alpha_count)))
    rows, expected = [], bytearray()
    for y in range(height):
        samples = [(x + y * 3) % entries for x in range(width)]
        if bad_index and y == 4:
            samples[-1] = entries
        bits = ''.join(format(sample, f'0{depth}b') for sample in samples)
        bits += str(padding) * (-len(bits) % 8)
        rows.append(bytes(int(bits[i:i + 8], 2) for i in range(0, len(bits), 8)))
        if not bad_index:
            for sample in samples:
                expected.extend(colors[sample * 3:sample * 3 + 3])
                if alpha_count >= 0:
                    expected.append(alpha[sample] if sample < len(alpha) else 255)
    filtered = bytearray()
    for y, row in enumerate(rows):
        filtered.append(y)
        for x, value in enumerate(row):
            a = row[x - 1] if x else 0
            b = rows[y - 1][x] if y else 0
            c = rows[y - 1][x - 1] if y and x else 0
            p = a + b - c
            candidates = [a, b, c]
            paeth = min(enumerate(candidates), key=lambda item: (abs(p - item[1]), item[0]))[1]
            filtered.append((value - [0, a, b, (a + b) // 2, paeth][y]) % 256)
    header = struct.pack('>IIBBBBB', width, height, depth, 3, 0, 0, 0)
    data = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', header) + chunk(b'PLTE', colors)
    if alpha_count >= 0:
        data += chunk(b'tRNS', alpha)
    data += chunk(b'IDAT', zlib.compress(filtered)) + chunk(b'IEND', b'')
    channels = 4 if alpha_count >= 0 else 3
    if not bad_index:
        with Image.open(io.BytesIO(data)) as image:
            assert image.size == (width, height)
            assert image.convert('RGBA' if channels == 4 else 'RGB').tobytes() == expected
    return data, bytes(expected), channels


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    options = parser.parse_args()
    rows = []
    for depth in [1, 2, 4, 8]:
        for width in [1, 17]:
            for padding in [0, 1]:
                for alpha_count in [-1, 1, 1 << depth]:
                    data, expected, channels = vector(depth, width, padding, alpha_count)
                    rows.append((data, expected, width, depth, channels, 1 << depth))
        data, expected, channels = vector(depth, 1, 1, 0, entries=1)
        rows.append((data, expected, 1, depth, channels, 1))
    for alpha_count in [-1, 1, 256]:
        data, expected, channels = vector(8, 257, 1, alpha_count)
        rows.append((data, expected, 257, 8, channels, 256))
    invalid = [vector(depth, 17, 1, 1, entries=1, bad_index=True)[0] for depth in [1, 2, 4, 8]]
    lines = ['import "std/hex";', '', 'pub struct Case { encoded: u8[], expected: u8[], width: i32, depth: i32, channels: i32, entries: i32 }', '',
             'pub function count(): i32 {', f'  return {len(rows)};', '}', '', 'pub function get(index: i32): Case {',
             '  let (data, pixels, width, depth, channels, entries) = match (index) {']
    for index, (data, expected, width, depth, channels, entries) in enumerate(rows):
        lines.append(f'    {index} => ("{data.hex()}", "{expected.hex()}", {width}, {depth}, {channels}, {entries}),')
    lines.extend(['    _ => ("", "", 0, 0, 0, 0),', '  };',
                  '  let Ok(encoded) = hex.decode(data) else {',
                  '    return Case { encoded: [], expected: [], width: 0, depth: 0, channels: 0, entries: 0 };', '  };',
                  '  let Ok(expected) = hex.decode(pixels) else {',
                  '    return Case { encoded: [], expected: [], width: 0, depth: 0, channels: 0, entries: 0 };', '  };',
                  '  return Case { encoded: encoded, expected: expected, width: width, depth: depth, channels: channels, entries: entries };', '}', '',
                  'pub function bad_index(index: i32): u8[] {', '  let text = match (index) {'])
    for index, data in enumerate(invalid):
        lines.append(f'    {index} => "{data.hex()}",')
    lines.extend(['    _ => "",', '  };', '  return match (hex.decode(text)) { Ok(v) => v, Err(_) => [] };', '}', ''])
    wide, _, _ = vector(8, 32769, 1, 1, height=1)
    lines.extend(['pub function wide_row(): u8[] {',
                  f'  return match (hex.decode("{wide.hex()}")) {{ Ok(v) => v, Err(_) => [] }};', '}', ''])
    source = '\n'.join(lines)
    if options.check:
        assert Path(__file__).with_name('png_indexed.fern').read_text() == source
    else:
        print(source, end='')


if __name__ == '__main__':
    main()
