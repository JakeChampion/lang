#!/usr/bin/env python3
"""Qualify native FLAC LPC output with FFmpeg and libFLAC."""

import argparse
import base64
import hashlib
import json
import math
from pathlib import Path
import random
import runpy
import struct
import subprocess
import tempfile
import zlib

ROOT = Path(__file__).resolve().parents[1]
DECODE = runpy.run_path(str(ROOT / "scripts/audio-reference/flac-independent.py"))[
    "decode"
]


def packed(value):
    return base64.b64encode(zlib.compress(value, 9)).decode()


def tone(depth, channels, frames):
    scale = (1 << (depth - 2)) - 1
    return [
        round(
            (math.sin(at * (0.047 + channel * 0.009)) + 0.25 * math.sin(at * 0.133))
            * scale
        )
        for at in range(frames)
        for channel in range(channels)
    ]


def cases():
    for depth in range(4, 33):
        yield f"depth-{depth}", depth, 1, 48000, 129, 8, tone(depth, 1, 129)
    for depth in [16, 24, 32]:
        for channels in range(2, 9):
            yield (
                f"layout-{depth}-{channels}",
                depth,
                channels,
                192000,
                257,
                8,
                tone(depth, channels, 257),
            )
    for order in [1, 2, 4, 8, 16, 32]:
        rng = random.Random(14807 + order)
        period = [rng.randint(-1000000, 1000000) for _ in range(order)]
        samples = []
        value = 0
        for at in range(1024):
            value = round(0.4 * value) + rng.randint(-1000000, 1000000)
            samples.append(value if order == 1 else period[at % order])
        yield f"order-{order}", 24, 1, 48000, 1024, order, samples
    for frames in [1, 2, 15, 16, 17, 33, 64, 513]:
        yield (
            f"short-{frames}",
            24,
            1,
            44100,
            max(16, min(frames, 256)),
            32,
            tone(24, 1, frames),
        )
    for shape in ["silence", "constant", "wasted", "wide-side", "noise"]:
        rng = random.Random(14807)
        samples = []
        for at in range(257):
            value = rng.randint(-2147483648, 2147483647)
            if shape == "silence":
                left, right = 0, 0
            elif shape == "constant":
                left, right = -2147483648, 2147483647
            elif shape == "wasted":
                left = tone(24, 1, 257)[at] * 256
                right = -left
            elif shape == "wide-side":
                left, right = value, -value - 1
            else:
                left, right = value, rng.randint(-2147483648, 2147483647)
            samples.extend([left, right])
        yield shape, 32, 2, 48000, 256, 32, samples
    rng = random.Random(14807033)
    period = [-2147483648, 2147483647] + [
        rng.randint(-2147483648, 2147483647) for _ in range(30)
    ]
    samples = [
        sample
        for at in range(1024)
        for sample in [period[at % 32], -period[at % 32] - 1]
    ]
    yield "wide-periodic", 32, 2, 48000, 1024, 32, samples


def first_offset(encoded):
    # Inspect the first subframe of a stream emitted by the native encoder.
    at = 42
    assert encoded[:8] == b"fLaC\x80\0\0\x22"
    code = encoded[at + 2] >> 4
    at += 4
    first = encoded[at]
    count = 1
    if first >= 192:
        count = 0
        while first & (128 >> count):
            count += 1
    at += count + (1 if code == 6 else 2 if code == 7 else 0) + 1
    return at


def first_kind(encoded):
    return (encoded[first_offset(encoded)] >> 1) & 63


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--encoder", required=True, type=Path)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    encoder = args.encoder.resolve()
    ffmpeg = subprocess.check_output(["ffmpeg", "-version"], text=True).splitlines()[0]
    rows, records = [], []
    with tempfile.TemporaryDirectory(prefix="flac-lpc-reference-") as directory:
        temp = Path(directory)
        for name, depth, channels, rate, block, order, samples in cases():
            raw = struct.pack("<" + "i" * len(samples), *samples)
            source, path = temp / "input.s32le", temp / "output.flac"
            source.write_bytes(raw)
            command = [
                str(encoder),
                str(source),
                str(path),
                str(depth),
                str(channels),
                str(rate),
                str(block),
            ]
            run = subprocess.run(
                command + [str(order)],
                check=True,
                capture_output=True,
                text=True,
                timeout=120,
            )
            if "leakcheck:" in run.stderr:
                assert "live_bytes=0" in run.stderr, (name, run.stderr)
            encoded = path.read_bytes()
            if name == "wide-periodic":
                at = first_offset(encoded)
                assert encoded[45] >> 4 == 10 and encoded[at] == 0
                assert encoded[at + 5] >> 1 == 63, (
                    "33-bit side channel must use LPC order 32"
                )
            pcm, version = DECODE(path, channels, depth, rate)
            assert pcm == samples, name
            ffmpeg_pcm = subprocess.check_output(
                ["ffmpeg", "-v", "error", "-i", str(path), "-f", "s32le", "-"],
                timeout=30,
            )
            assert ffmpeg_pcm == struct.pack(
                "<" + "i" * len(samples), *(value << (32 - depth) for value in samples)
            ), name
            digest = hashlib.md5(
                b"".join(
                    (value & ((1 << (((depth + 7) // 8) * 8)) - 1)).to_bytes(
                        (depth + 7) // 8, "little"
                    )
                    for value in samples
                )
            ).digest()
            assert encoded[26:42] == digest, name
            subprocess.run(
                command, check=True, capture_output=True, text=True, timeout=120
            )
            fixed_bytes = path.stat().st_size
            assert len(encoded) <= fixed_bytes, (name, "LPC enlarged fixed-only output")
            record = {
                "name": name,
                "depth": depth,
                "channels": channels,
                "rate": rate,
                "block": block,
                "order": order,
                "frames": len(samples) // channels,
                "pcm_sha256": hashlib.sha256(raw).hexdigest(),
                "encoded_sha256": hashlib.sha256(encoded).hexdigest(),
                "encoded_bytes": len(encoded),
                "fixed_bytes": fixed_bytes,
                "first_kind": first_kind(encoded),
            }
            records.append(record)
            rows.append(
                '    Vector { name: "'
                + name
                + '", depth: '
                + str(depth)
                + ", channels: "
                + str(channels)
                + ", rate: "
                + str(rate)
                + ", block: "
                + str(block)
                + ", order: "
                + str(order)
                + ', pcm: "'
                + packed(raw)
                + '", encoded: "'
                + packed(encoded)
                + '" },'
            )
    assert next(row for row in records if row["name"] == "order-32")["first_kind"] == 63
    assert any(
        row["first_kind"] >= 32 and row["encoded_bytes"] < row["fixed_bytes"]
        for row in records
    )
    text = (
        "// Generated by scripts/gen-flac-lpc-vectors.py.\n// Independent decode: "
        + ffmpeg
        + ", libFLAC "
        + version
        + ".\n"
    )
    text += "pub struct Vector { name: string, depth: i32, channels: i32, rate: i32, block: i32, order: i32, pcm: string, encoded: string }\n\npub function all(): Vector[] {\n  return [\n"
    text += "\n".join(rows) + "\n  ];\n}\n"
    text += (
        "\npub function smoke(): Vector {\n  return "
        + rows[12].strip()[0:-1]
        + ";\n}\n"
    )
    sources = [
        ROOT / "internal/stdlib/std/audio/flac" / name
        for name in [
            "encoder.fern.md",
            "plan.fern.md",
            "lpc.fern.md",
            "bitwriter.fern.md",
            "checksum.fern.md",
        ]
    ]
    metadata = {
        "ffmpeg": ffmpeg,
        "libFLAC": version,
        "sources": {
            str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in sources
        },
        "vectors": records,
    }
    for path, content in [
        (ROOT / "tests/stdlib/flac_lpc_vectors.fern", text),
        (
            ROOT / "scripts/audio-reference/flac-lpc-vectors.json",
            json.dumps(metadata, indent=2) + "\n",
        ),
    ]:
        if args.check:
            assert path.read_text() == content, str(path) + " differs"
        else:
            path.write_text(content)
    print(f"{len(records)} FLAC LPC cases pass FFmpeg, libFLAC {version}, and PCM MD5")


if __name__ == "__main__":
    main()
