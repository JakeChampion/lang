"""Generate finite exact rational observations for nominal centered component grids."""

import argparse
from fractions import Fraction
import fractions
import gzip
import hashlib
import json
import math
from pathlib import Path
import platform
import random
import struct
import sys


def sha(data):
    return hashlib.sha256(data).hexdigest()


def rounded(value):
    value += Fraction(1, 2)
    return value.numerator // value.denominator


def reference(data, width, height, stride, x, y, horizontal, vertical, max_h, max_v, mode):
    u = (Fraction(x) + Fraction(1, 2)) * Fraction(horizontal, max_h) - Fraction(1, 2)
    v = (Fraction(y) + Fraction(1, 2)) * Fraction(vertical, max_v) - Fraction(1, 2)

    def at(px, py):
        return data[max(0, min(height - 1, py)) * stride + max(0, min(width - 1, px))]

    if mode == 0:
        return at(rounded(u), rounded(v))
    left, top = math.floor(u), math.floor(v)
    fx, fy = u - left, v - top
    value = (at(left, top) * (1 - fx) + at(left + 1, top) * fx) * (1 - fy)
    value += (at(left, top + 1) * (1 - fx) + at(left + 1, top + 1) * fx) * fy
    return rounded(value)


def make_case(index, frame_w, frame_h, max_h, max_v, horizontal, vertical, row, mode, pattern):
    width = math.ceil(Fraction(frame_w * horizontal, max_h))
    height = math.ceil(Fraction(frame_h * vertical, max_v))
    stride = width + index % 3
    source = bytearray([251] * (stride * height))
    generator = random.Random(13626 + index)
    for y in range(height):
        for x in range(width):
            value = (x * 37 + y * 83) % 256
            if pattern == 1:
                value = 255 if (x + y) % 2 else 0
            elif pattern == 2:
                value = 255 if (x, y) == (width // 2, height // 2) else 0
            elif pattern == 3:
                value = generator.randrange(256)
            elif pattern == 4:
                value = 0
            elif pattern == 5:
                value = 255
            elif pattern == 6:
                value = x + y
            source[y * stride + x] = value
    encoded = struct.pack(">13i", frame_w, frame_h, max_h, max_v, row, mode, 201,
                          horizontal, vertical, width, height, stride, len(source)) + source
    output = bytes(reference(source, width, height, stride, x, row, horizontal, vertical, max_h, max_v, mode)
                   for x in range(frame_w))
    return {"name": f"row-{index:04d}", "width": frame_w, "mode": "nearest" if mode == 0 else "bilinear",
            "input_hex": encoded.hex(), "samples_hex": output.hex(), "input_sha256": sha(encoded), "samples_sha256": sha(output)}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("bundle", type=Path)
    args = parser.parse_args()
    paths = {"generator": Path(__file__).resolve(), "Python": Path(sys.executable).resolve(),
             "fractions": Path(fractions.__file__).resolve(), "random": Path(random.__file__).resolve()}
    hashes = {name: sha(path.read_bytes()) for name, path in paths.items()}
    cases = []
    for max_h in range(1, 5):
        for max_v in range(1, 5):
            for horizontal in range(1, max_h + 1):
                for vertical in range(1, max_v + 1):
                    for width, height in [(1, 1), (2, 3), (7, 5), (17, 9)]:
                        for row in sorted({0, height // 2, height - 1}):
                            for mode in [0, 1]:
                                pattern = (max_h + max_v + horizontal + vertical + width + height) % 4
                                cases.append(make_case(len(cases), width, height, max_h, max_v, horizontal, vertical, row, mode, pattern))
    for mode in [0, 1]:
        for row in [0, 1, 2]:
            cases.append(make_case(len(cases), 257, 3, 2, 2, 1, 1, row, mode, 1))
        for pattern in [4, 5]:
            for row in [0, 4, 8]:
                cases.append(make_case(len(cases), 17, 9, 4, 4, 1, 1, row, mode, pattern))
        for row in range(4):
            cases.append(make_case(len(cases), 4, 4, 2, 2, 1, 1, row, mode, 6))
        cases.append(make_case(len(cases), 65535, 1, 4, 1, 1, 1, 0, mode, 0))
        cases.append(make_case(len(cases), 1, 65535, 1, 4, 1, 1, 65534, mode, 0))
    if hashes != {name: sha(path.read_bytes()) for name, path in paths.items()}:
        raise ValueError("reference source or tool changed")
    document = {"schema": 1, "reference": {"Python": platform.python_version(), "sha256": hashes,
                "placement_reference": {"url": "https://www.w3.org/Graphics/JPEG/jfif3.pdf", "page": 4,
                                        "sha256": "1135325aa12ee6b3def5a374fecdf3d6d0de88dc4f205af7de8d25a9c949e173"},
                "method": "Exact Fraction nominal-factor centers; clamped edge extension; explicit nearest or one final bilinear rounding with upward half ties.",
                "scope": "Finite mathematical raw component rows, not encoded color interpretation, codec parity, broad compatibility, perceptual quality, or production."},
                "samples": sum(case["width"] for case in cases), "cases": cases}
    args.bundle.parent.mkdir(parents=True, exist_ok=True)
    args.bundle.write_bytes(gzip.compress(json.dumps(document, separators=(",", ":")).encode(), mtime=0))
    print(f"created {len(cases)} rows and {document['samples']} samples")


if __name__ == "__main__":
    main()
