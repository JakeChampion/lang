#!/usr/bin/env python3
"""Regenerate or check the exact 32-sample xorshift stream tables."""
import argparse
from pathlib import Path
import re


def random(seed):
    seed ^= seed << 13 & 0xffffffff
    seed ^= seed >> 17
    seed ^= seed << 5 & 0xffffffff
    return seed


def tables():
    streams = []
    for byte in range(4):
        for value in range(256):
            seed, sign, pattern = value << (8 * byte), 0, 0
            for sample in range(32):
                seed = random(seed)
                sign ^= seed >> 31
                pattern |= sign << sample
            streams.append(seed << 32 | pattern)
    factors = []
    for pattern in range(16):
        for lane in range(4):
            factors.extend((0, 0, 128, 191 if pattern >> lane & 1 else 63))
    return streams, factors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write", action="store_true")
    args = parser.parse_args()
    path = Path(__file__).resolve().parents[1] / "noise_jump.fern"
    text = path.read_text()
    streams, factors = tables()
    if args.write:
        text = text[:text.index("// Each word holds")]
        text += "// Each word holds the seed after 32 samples and their cumulative sign bits.\n"
        text += "// Four byte-indexed table lookups combine by XOR because xorshift is linear.\n"
        text += "pub const STREAM32: u64[] = [\n"
        for start in range(0, len(streams), 4):
            text += "  " + ", ".join(f"{x}u64" for x in streams[start:start + 4]) + ",\n"
        text += "];\n\n// Four exact unit factors for each low nybble.\n"
        text += "pub const SIGN_FACTORS: u8[] = [\n"
        for start in range(0, len(factors), 16):
            text += "  " + ", ".join(f"{x}u8" for x in factors[start:start + 16]) + ",\n"
        path.write_text(text + "];\n")
    for name, suffix, expected in (("STREAM32", "u64", streams), ("SIGN_FACTORS", "u8", factors)):
        body = text.split(f"pub const {name}:", 1)[1].split("[", 2)[2].split("];", 1)[0]
        actual = [int(x) for x in re.findall(rf"(\d+){suffix}\b", body)]
        if actual != expected:
            raise SystemExit(f"{name} differs from the scalar xorshift recurrence; regenerate with --write")
    print("ok - 32-sample xorshift tables match the scalar recurrence")


if __name__ == "__main__":
    main()
