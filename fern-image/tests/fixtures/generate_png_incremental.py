"""Generate wide PNG fixtures with an independent offline reference."""

import hashlib
import io
import struct
import zlib
from pathlib import Path

from PIL import Image


def expected(y, index):
    if y == 0:
        return 255 if index < 256 else 240 if index == 256 else 0
    return (index * 37 + y * 11 + index // 251) % 256


def paeth(a, b, c):
    estimate = a + b - c
    distances = [abs(estimate - a), abs(estimate - b), abs(estimate - c)]
    return [a, b, c][distances.index(min(distances))]


def chunk(kind, data):
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))


def main():
    parts = ['import "std/hex";\n\n// Offline zlib fixtures use all five filters and a zero Adler A at the first chunk boundary.\n']
    for name, channels, color in [("wide_rgb", 3, 2), ("wide_rgba", 4, 6)]:
        width, height = 11111, 5
        stride = width * channels
        rows = [bytes(expected(y, index) for index in range(stride)) for y in range(height)]
        raw = bytearray()
        for y, row in enumerate(rows):
            raw.append(y)
            for index, value in enumerate(row):
                a = row[index - channels] if index >= channels else 0
                b = rows[y - 1][index] if y else 0
                c = rows[y - 1][index - channels] if y and index >= channels else 0
                prediction = [0, a, b, (a + b) // 2, paeth(a, b, c)][y]
                raw.append((value - prediction) % 256)
        assert zlib.adler32(raw[:32768]) & 65535 == 0
        encoded = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, color, 0, 0, 0))
                   + chunk(b"IDAT", zlib.compress(raw, 6)) + chunk(b"IEND", b""))
        with Image.open(io.BytesIO(encoded)) as image:
            assert image.size == (width, height) and image.tobytes() == b"".join(rows)
        print(name, "PNG", len(encoded), "filtered", len(raw), "PNG_sha", hashlib.sha256(encoded).hexdigest(),
              "pixels_sha", hashlib.sha256(b"".join(rows)).hexdigest())
        parts.append(f'pub function {name}(): u8[] {{\n  let Ok(bytes) = hex.decode("{encoded.hex()}") else {{ return []; }};\n  return bytes;\n}}\n')
    invalid = zlib.compress(bytes([0, 1, 2, 3, 4, 5, 6, 5, 7, 8, 9, 10, 11, 12]), 6)
    invalid = invalid[:-1] + bytes([invalid[-1] ^ 1])
    broken = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 2, 2, 8, 2, 0, 0, 0))
              + chunk(b"IDAT", invalid) + chunk(b"IEND", b""))
    try:
        with Image.open(io.BytesIO(broken)) as image:
            image.load()
    except OSError:
        pass
    else:
        raise ValueError("expected malformed PNG rejection")
    parts.append(f'pub function bad_filter_and_checksum(): u8[] {{\n  let Ok(bytes) = hex.decode("{broken.hex()}") else {{ return []; }};\n  return bytes;\n}}\n')
    parts.append('''pub function expected(y: i32, at: i32): u8 {
  if (y == 0) {
    if (at < 256) { return 255 as u8; }
    if (at == 256) { return 240 as u8; }
    return 0 as u8;
  }
  return ((at * 37 + y * 11 + at / 251) % 256) as u8;
}
''')
    Path(__file__).with_name("png_incremental.fern").write_text("\n".join(parts))


if __name__ == "__main__":
    main()
