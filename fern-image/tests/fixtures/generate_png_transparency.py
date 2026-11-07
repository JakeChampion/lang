"""Create sample-key PNG fixtures with independent filters and Pillow checks."""

import argparse
import io
from pathlib import Path
import struct
import zlib

from PIL import Image


def chunk(kind, data):
    return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))


def vector(depth, color_type, width, key, padding=0, high=False, height=5):
    channels = 1 if color_type == 0 else 3
    maximum = (1 << depth) - 1
    keys = [key] if channels == 1 else [key, (key + 71) % 256, (key + 139) % 256]
    rows, expected = [], bytearray()
    for y in range(height):
        samples = []
        for x in range(width):
            values = list(keys)
            if (x + y) % 4 != 0:
                at = (x + y) % channels
                values[at] = (values[at] + 1 + (x + y) % maximum) & maximum
            samples.extend(values)
            rgb = values if channels == 3 else [values[0] * 255 // maximum] * 3
            expected.extend([*rgb, 0 if values == keys else 255])
        bits = ''.join(format(sample, f'0{depth}b') for sample in samples)
        bits += str(padding) * (-len(bits) % 8)
        rows.append(bytes(int(bits[i:i + 8], 2) for i in range(0, len(bits), 8)))
    filtered = bytearray()
    for y, row in enumerate(rows):
        kind = y % 5
        filtered.append(kind)
        for x, value in enumerate(row):
            a = row[x - channels] if x >= channels else 0
            b = rows[y - 1][x] if y else 0
            c = rows[y - 1][x - channels] if y and x >= channels else 0
            p = a + b - c
            candidates = [a, b, c]
            paeth = min(enumerate(candidates), key=lambda item: (abs(p - item[1]), item[0]))[1]
            filtered.append((value - [0, a, b, (a + b) // 2, paeth][kind]) % 256)
    header = struct.pack('>IIBBBBB', width, height, depth, color_type, 0, 0, 0)
    prefix = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', header)
    tail = chunk(b'IDAT', zlib.compress(filtered)) + chunk(b'IEND', b'')
    canonical = b''.join(struct.pack('>H', value) for value in keys)
    normalized = prefix + chunk(b'tRNS', canonical) + tail
    with Image.open(io.BytesIO(normalized)) as image:
        assert image.size == (width, height)
        # Pillow expands packed gray samples but keeps the unscaled tRNS key.
        if color_type == 0 and depth in [2, 4]:
            image.info['transparency'] = key * 255 // maximum
        assert image.convert('RGBA').tobytes() == expected
    stored = b''.join(struct.pack('>H', value | (65535 ^ maximum) if high else value) for value in keys)
    return prefix + chunk(b'tRNS', stored) + tail, bytes(expected), normalized


def cases():
    for depth, color_type in [(1, 0), (2, 0), (4, 0), (8, 0), (8, 2)]:
        for key in sorted({0, ((1 << depth) - 1) // 2, (1 << depth) - 1}):
            for high in [False, True]:
                for width in [1, 17]:
                    data, expected, normalized = vector(depth, color_type, width, key, 1, high)
                    yield data, expected, normalized, width, depth, color_type
    for color in [0, 2]:
        data, expected, normalized = vector(8, color, 257, 127, 0, True)
        yield data, expected, normalized, 257, 8, color


def invalid():
    for color in [0, 2]:
        data, _, _ = vector(8, color, 17, 127)
        offset, parts = 8, []
        while offset < len(data):
            size = struct.unpack_from('>I', data, offset)[0]
            parts.append((data[offset + 4:offset + 8], data[offset + 8:offset + 8 + size]))
            offset += size + 12
        header, trns, idat, iend = parts
        for length in range(9):
            if length != len(trns[1]):
                yield [header, (b'tRNS', bytes(length)), idat, iend], 1
        yield [header, trns, trns, idat, iend], 2
        yield [header, idat, trns, iend], 2
        yield [trns, header, idat, iend], 2
        yield [header, trns, (b'PLTE', bytes([1, 2, 3])), idat, iend], 2
        alpha_header = bytearray(header[1])
        alpha_header[9] = 4 if color == 0 else 6
        yield [(b'IHDR', alpha_header), trns, idat, iend], 3
        filtered = bytearray(zlib.decompress(idat[1]))
        filtered[4 * (len(filtered) // 5)] = 5
        yield [header, trns, (b'IDAT', zlib.compress(filtered)), iend], 5
        compressed = bytearray(zlib.compress(filtered))
        compressed[-1] ^= 1
        yield [header, trns, (b'IDAT', compressed), iend], 4


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    options = parser.parse_args()
    rows = list(cases())
    lines = ['import "std/hex";', '',
             'pub struct Case { encoded: u8[], expected: u8[], width: i32, depth: i32, color_type: i32 }', '',
             'pub function count(): i32 {', f'  return {len(rows)};', '}', '',
             'pub function get(index: i32): Case {',
             '  let (data, pixels, width, depth, color_type) = match (index) {']
    for index, (data, expected, _, width, depth, color_type) in enumerate(rows):
        lines.append(f'    {index} => ("{data.hex()}", "{expected.hex()}", {width}, {depth}, {color_type}),')
    lines.extend(['    _ => ("", "", 0, 0, 0),', '  };',
                  '  let encoded = match (hex.decode(data)) { Ok(v) => v, Err(_) => [] };',
                  '  let expected = match (hex.decode(pixels)) { Ok(v) => v, Err(_) => [] };',
                  '  return Case { encoded: encoded, expected: expected, width: width, depth: depth, color_type: color_type };', '}', ''])
    bad = list(invalid())
    lines.extend(['pub function invalid_count(): i32 {', f'  return {len(bad)};', '}', '',
                  'pub function invalid(index: i32): (u8[], i32) {', '  let (text, error) = match (index) {'])
    for index, (parts, error) in enumerate(bad):
        data = b'\x89PNG\r\n\x1a\n' + b''.join(chunk(kind, payload) for kind, payload in parts)
        lines.append(f'    {index} => ("{data.hex()}", {error}),')
    lines.extend(['    _ => ("", 0),', '  };',
                  '  return (match (hex.decode(text)) { Ok(v) => v, Err(_) => [] }, error);', '}', ''])
    source = '\n'.join(lines)
    if options.check:
        assert Path(__file__).with_name('png_transparency.fern').read_text() == source
    else:
        print(source, end='')


if __name__ == '__main__':
    main()
