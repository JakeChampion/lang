"""Build grayscale PNG fixtures and verify pixels with offline Pillow."""

import argparse
import io
import struct
import zlib
from pathlib import Path

from PIL import Image


def chunk(kind, data):
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))


def png(width, height, channels, raw, filters, *, depth=8, interlace=0, suffix=b"", extra=b""):
    stride = width * channels
    filtered = bytearray()
    for y in range(height):
        filter_id = filters[y % len(filters)]
        filtered.append(filter_id)
        for x in range(stride):
            at = y * stride + x
            a = raw[at - channels] if x >= channels else 0
            b = raw[at - stride] if y else 0
            c = raw[at - stride - channels] if y and x >= channels else 0
            p = a + b - c
            distances = [abs(p - a), abs(p - b), abs(p - c)]
            prediction = [0, a, b, (a + b) // 2, [a, b, c][distances.index(min(distances))]][min(filter_id, 4)]
            filtered.append((raw[at] - prediction) % 256)
    header = struct.pack(">IIBBBBB", width, height, depth, 0 if channels == 1 else 4, 0, 0, interlace)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + extra
            + chunk(b"IDAT", zlib.compress(filtered) + suffix) + chunk(b"IEND", b""))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    options = parser.parse_args()
    gray = bytes([0, 255, 17, 200, 3, 128, 12, 254, 1])
    alpha = bytes([255, 0, 128, 17, 255, 0, 254, 1, 200])
    fixtures = {}
    for channels in [1, 2]:
        raw = gray if channels == 1 else bytes(value for pair in zip(gray, alpha, strict=True) for value in pair)
        for filter_id in range(5):
            encoded = png(3, 3, channels, raw, [filter_id])
            with Image.open(io.BytesIO(encoded)) as image:
                assert image.mode == ("L" if channels == 1 else "LA")
                assert image.size == (3, 3) and image.tobytes() == raw
                expected = bytes(value for index, g in enumerate(gray)
                                 for value in ([g, g, g] if channels == 1 else [g, g, g, alpha[index]]))
                assert image.convert("RGB" if channels == 1 else "RGBA").tobytes() == expected
            fixtures[f"gray{channels}_{filter_id}"] = encoded
        fixtures[f"bad_final_{channels}"] = png(3, 3, channels, raw, [0, 1, 5])
        fixtures[f"trailing_{channels}"] = png(3, 3, channels, raw, [4], suffix=b"x")
        fixtures[f"depth_{channels}"] = png(3, 3, channels, raw, [0], depth=16)
        fixtures[f"interlace_{channels}"] = png(3, 3, channels, raw, [0], interlace=1)
        fixtures[f"transparency_{channels}"] = png(3, 3, channels, raw, [0], extra=chunk(b"tRNS", b"\0\0"))
        width, height = 17000, 5
        wide = bytes((x * 37 + y * 11 + x // 251) % 256
                     for y in range(height) for x in range(width * channels))
        encoded = png(width, height, channels, wide, list(range(5)))
        with Image.open(io.BytesIO(encoded)) as image:
            assert image.tobytes() == wide
        fixtures[f"wide_{channels}"] = encoded
    source = 'import "std/hex";\n\n// Offline Pillow verifies samples from independent PNG filter equations.\n'
    for index, (name, data) in enumerate(fixtures.items()):
        if index:
            source += '\n'
        source += (f'pub function {name}(): u8[] {{\n'
                   f'  let Ok(bytes) = hex.decode("{data.hex()}") else {{\n'
                   '    return [];\n  };\n  return bytes;\n}\n')
    if options.check:
        actual = Path(__file__).with_name("png_grayscale.fern").read_text()
        if actual != source:
            raise ValueError("grayscale fixtures differ from the reference generator")
    else:
        print(source, end="")


if __name__ == "__main__":
    main()
