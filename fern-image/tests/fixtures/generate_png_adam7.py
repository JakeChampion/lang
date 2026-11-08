"""Check Adam7 samples with independent tile extraction and offline Pillow."""

import argparse
import io
from itertools import islice
from pathlib import Path
import struct
import zlib

from PIL import Image

TILE = (
    (1, 6, 4, 6, 2, 6, 4, 6), (7,) * 8,
    (5, 6, 5, 6, 5, 6, 5, 6), (7,) * 8,
    (3, 6, 4, 6, 3, 6, 4, 6), (7,) * 8,
    (5, 6, 5, 6, 5, 6, 5, 6), (7,) * 8,
)
MODES = [(1, 0), (2, 0), (4, 0), (8, 0), (8, 2), (1, 3), (2, 3), (4, 3), (8, 3), (8, 4), (8, 6)]
SIZES = [(1, 1), (1, 9), (9, 1), (2, 3), (8, 8), (17, 9)]


def chunk(kind, payload):
    return struct.pack('>I', len(payload)) + kind + payload + struct.pack('>I', zlib.crc32(kind + payload))


def vector(depth, color, width, height, variant=0):
    channels = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[color]
    maximum = (1 << depth) - 1
    key = tuple((c * 71 + maximum // 2) & maximum for c in range(channels))
    transparent = color in (0, 2, 3) and variant % 2 == 1
    palette = bytes(v for i in range(maximum + 1) for v in ((i * 71) % 256, (i * 43 + 17) % 256, (i * 19 + 59) % 256)) if color == 3 else b''
    alpha = bytes((i * 37) % 256 for i in range(maximum)) if color == 3 and transparent else b''
    rows, expected = [], bytearray()
    for y in range(height):
        row = []
        for x in range(width):
            pixel = tuple((x * 37 + y * 19 + c * 71 + x * y % 13) & maximum for c in range(channels))
            if transparent and color in (0, 2) and (x + y) % 5 == 0:
                pixel = key
            row.append(pixel)
            if color == 3:
                index = pixel[0]
                values = list(palette[index * 3:index * 3 + 3])
                if transparent:
                    values.append(alpha[index] if index < len(alpha) else 255)
            elif color in (0, 4):
                values = [pixel[0] * 255 // maximum] * 3
                if color == 4:
                    values.append(pixel[1])
                elif transparent:
                    values.append(0 if pixel == key else 255)
            else:
                values = list(pixel)
                if transparent:
                    values.append(0 if pixel == key else 255)
            expected.extend(values)
        rows.append(row)
    filtered = bytearray()
    boundaries = []
    for number in range(1, 8):
        reduced = []
        for y in range(height):
            samples = [value for x in range(width) if TILE[y % 8][x % 8] == number for value in rows[y][x]]
            if samples:
                bits = ''.join(format(value, f'0{depth}b') for value in samples)
                bits += str(variant % 2) * (-len(bits) % 8)
                reduced.append(bytes(int(bits[i:i + 8], 2) for i in range(0, len(bits), 8)))
        if reduced:
            boundaries.append(len(filtered))
        for y, row in enumerate(reduced):
            kind = (number + y + variant) % 5
            filtered.append(kind)
            bpp = max(1, channels * depth // 8)
            for i, value in enumerate(row):
                a = row[i - bpp] if i >= bpp else 0
                b = reduced[y - 1][i] if y else 0
                c = reduced[y - 1][i - bpp] if y and i >= bpp else 0
                candidates = [a, b, c]
                p = a + b - c
                paeth = min(enumerate(candidates), key=lambda v: (abs(p - v[1]), v[0]))[1]
                filtered.append((value - [0, a, b, (a + b) // 2, paeth][kind]) % 256)
    header = struct.pack('>IIBBBBB', width, height, depth, color, 0, 0, 1)
    prefix = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', header)
    if palette:
        prefix += chunk(b'PLTE', palette)
    if transparent:
        prefix += chunk(b'tRNS', alpha if color == 3 else b''.join(struct.pack('>H', v) for v in key))
    data = prefix + chunk(b'IDAT', zlib.compress(filtered)) + chunk(b'IEND', b'')
    output_channels = 4 if color in (4, 6) or transparent else 3
    with Image.open(io.BytesIO(data)) as image:
        assert image.size == (width, height)
        if color == 0 and depth in (2, 4) and transparent:
            image.info['transparency'] = key[0] * 255 // maximum
        assert image.convert('RGBA' if output_channels == 4 else 'RGB').tobytes() == expected
    return dict(data=data, expected=bytes(expected), width=width, height=height, channels=output_channels,
                filtered=bytes(filtered), boundaries=boundaries, prefix=prefix)


def cases():
    for variant, (w, h) in enumerate(SIZES):
        for depth, color in MODES:
            yield vector(depth, color, w, h, variant)


def malformed():
    v = vector(8, 6, 17, 9)
    for at in v['boundaries']:
        raw = bytearray(v['filtered'])
        raw[at] = 5
        yield v['prefix'] + chunk(b'IDAT', zlib.compress(raw)) + chunk(b'IEND', b''), 'Scanline'
    yield v['prefix'] + chunk(b'IDAT', zlib.compress(v['filtered'][:-1])) + chunk(b'IEND', b''), 'Scanline'
    yield v['prefix'] + chunk(b'IDAT', zlib.compress(v['filtered'] + b'\0')) + chunk(b'IEND', b''), 'Compressed'
    yield v['prefix'] + chunk(b'IDAT', zlib.compress(v['filtered']) + b'\0') + chunk(b'IEND', b''), 'Compressed'
    header = struct.pack('>IIBBBBB', 17, 9, 8, 6, 0, 0, 2)
    yield b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', header) + chunk(b'IDAT', zlib.compress(v['filtered'])) + chunk(b'IEND', b''), 'Unsupported'
    v = vector(4, 3, 17, 9)
    prefix = v['prefix'][:33] + chunk(b'PLTE', bytes([0, 0, 0]))
    yield prefix + chunk(b'IDAT', zlib.compress(v['filtered'])) + chunk(b'IEND', b''), 'Scanline'


def source(limit=None):
    valid = list(cases()) if limit is None else list(islice(cases(), limit))
    bad = list(malformed())
    lines = ['import "std/hex";', 'import "../../packages/codecs/png/png";', '',
             'pub struct Case { encoded: u8[], expected: u8[], width: i32, height: i32, channels: i32, filtered_bytes: i32 }', '',
             'pub function count(): i32 {', f'  return {len(valid)};', '}', '', 'pub function get(index: i32): Case {',
             '  let (data, pixels, width, height, channels, filtered) = match (index) {']
    for i, v in enumerate(valid):
        lines.append(f'    {i} => ("{v["data"].hex()}", "{v["expected"].hex()}", {v["width"]}, {v["height"]}, {v["channels"]}, {len(v["filtered"])}),')
    lines += ['    _ => ("", "", 0, 0, 0, 0),', '  };',
              '  let Ok(encoded) = hex.decode(data) else {', '    return Case { encoded: [], expected: [], width: 0, height: 0, channels: 0, filtered_bytes: 0 };', '  };',
              '  let Ok(expected) = hex.decode(pixels) else {', '    return Case { encoded: [], expected: [], width: 0, height: 0, channels: 0, filtered_bytes: 0 };', '  };',
              '  return Case { encoded: encoded, expected: expected, width: width, height: height, channels: channels, filtered_bytes: filtered };', '}', '',
              'pub function bad_count(): i32 {', f'  return {len(bad)};', '}', '', 'pub function bad(index: i32): (u8[], png.PngError) {', '  let (text, error) = match (index) {']
    for i, (data, error) in enumerate(bad):
        lines.append(f'    {i} => ("{data.hex()}", png.{error}),')
    lines += ['    _ => ("", png.Signature),', '  };', '  let Ok(encoded) = hex.decode(text) else {', '    return ([], png.Signature);', '  };', '  return (encoded, error);', '}', '']
    return '\n'.join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--limit', type=int)
    args = parser.parse_args()
    text = source(args.limit)
    if args.check:
        assert Path(__file__).with_name('png_adam7.fern').read_text() == text
    else:
        print(text, end='')


if __name__ == '__main__':
    main()
