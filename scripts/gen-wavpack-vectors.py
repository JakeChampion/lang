#!/usr/bin/env python3
"""Freeze lossless WavPack files and independently checked PCM."""

import argparse
import base64
import hashlib
import json
import math
from pathlib import Path
import random
import struct
import subprocess
import sys
import tempfile
import zlib

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts" / "audio-reference"))
from wavpack_reference import encode, verify

PIN = "4827b9889665b937b6ed71b9c6c0123152cd7a02"


def cases():
    rng = random.Random(14686)
    vectors = []
    for bits in [8, 12, 16, 20, 24, 32]:
        for channels in [1, 2]:
            for flags in [0, 0x200, 0x800, 0x1000, 0x40000000]:
                bound = 1 << (bits - 1)
                padding = ((bits + 7) // 8) * 8 - bits
                samples = [
                    rng.randrange(-bound, bound) << padding
                    for _ in range(513 * channels)
                ]
                vectors.append(
                    (
                        f"random-{bits}-{channels}-{flags}",
                        samples,
                        channels,
                        bits,
                        flags,
                        False,
                    )
                )
    for name in ["zero", "ones", "constant", "ramp", "same", "music"]:
        for channels in [1, 2]:
            samples = []
            for i in range(513):
                for ch in range(channels):
                    value = {
                        "zero": 0,
                        "ones": -1,
                        "constant": 314159,
                        "ramp": i * 2000 - 400000,
                        "same": int(math.sin(i * 0.03) * 6000000),
                        "music": int(
                            (
                                math.sin(i * 0.03 + ch * 0.5)
                                + math.sin(i * 0.11 + ch * 0.05)
                            )
                            * 0.45
                            * 8388607
                        ),
                    }[name]
                    samples.append(value)
            vectors.append((f"{name}-{channels}", samples, channels, 24, 0, False))
    specials = [
        0,
        0x80000000,
        0x3F800000,
        0xBF800000,
        1,
        0x80000001,
        0x7F800000,
        0xFF800000,
        0x7FC00123,
        0xFFC12345,
        0x00800000,
        0x007FFFFF,
    ]
    for channels in range(3, 9):
        for bits in [16, 24, 32]:
            bound = 1 << (bits - 1)
            samples = [rng.randrange(-bound, bound) for _ in range(259 * channels)]
            vectors.append(
                (f"multichannel-{bits}-{channels}", samples, channels, bits, 0, False)
            )
    for channels in [1, 2]:
        for name, values in [
            ("special", specials * 20),
            (
                "sine",
                [
                    struct.unpack("<I", struct.pack("<f", math.sin(i * 0.1)))[0]
                    for i in range(514)
                ],
            ),
        ]:
            values = values[: len(values) // channels * channels]
            samples = [x if x < 0x80000000 else x - 0x100000000 for x in values]
            vectors.append((f"float-{name}-{channels}", samples, channels, 32, 0, True))
    for channels in [1, 2, 3, 6, 8]:
        samples = [
            rng.randrange(-2147483648, 2147483648) for _ in range(259 * channels)
        ]
        vectors.append((f"float-random-{channels}", samples, channels, 32, 0, True))
    for bits in [1, 2, 4, 7]:
        for channels in [1, 2]:
            bound = 1 << (bits - 1)
            samples = [
                rng.randrange(-bound, bound) << (8 - bits)
                for _ in range(131 * channels)
            ]
            vectors.append(
                (f"small-depth-{bits}-{channels}", samples, channels, bits, 0, False)
            )
    for frames in [1, 2, 3, 7, 19, 127, 128, 129]:
        samples = [((i * 71) % 65536) - 32768 for i in range(frames)]
        vectors.append((f"short-16-1-{frames}", samples, 1, 16, 0, False))
    vectors.append(
        ("short-24-2-19", [i * 7919 - 130000 for i in range(38)], 2, 24, 0, False)
    )
    vectors.append(
        ("seek-24-2-35", [i * 7919 - 130000 for i in range(70)], 2, 24, 0, False)
    )
    values = specials[:12]
    vectors.append(
        (
            "short-float-2-6",
            [x if x < 0x80000000 else x - 0x100000000 for x in values],
            2,
            32,
            0,
            True,
        )
    )
    for channels in [1, 2, 6, 8]:
        samples = [
            rng.randrange(-2147483648, 2147483648) for _ in range(259 * channels)
        ]
        vectors.append(
            (f"float-optimized-{channels}", samples, channels, 32, 0x40000000, True)
        )
    for tag in ["id3v1", "apev1", "apev2", "apev2-header", "apev2-header-id3v1"]:
        vectors.append(
            ("tag-" + tag, [i * 7919 - 130000 for i in range(38)], 2, 24, 0, False)
        )
    for position in ["prefix", "tail"]:
        vectors.append(
            (
                "metadata-" + position + "-16",
                [i * 71 - 1000 for i in range(14)],
                2,
                16,
                0,
                False,
            )
        )
        vectors.append(
            (
                "metadata-" + position + "-float",
                [x if x < 0x80000000 else x - 0x100000000 for x in specials],
                2,
                32,
                0,
                True,
            )
        )
    vectors.append(
        (
            "metadata-tail-zero-total",
            [i * 71 - 1000 for i in range(14)],
            2,
            16,
            0,
            False,
        )
    )
    return vectors


def tagged(encoded, name):
    if name == "metadata-tail-zero-total":
        at = 0
        while at < len(encoded):
            size = struct.unpack_from("<I", encoded, at + 4)[0] + 8
            if struct.unpack_from("<I", encoded, at + 20)[0] == 0:
                assert at + size == len(encoded) and encoded[-6] == 47
                metadata = bytearray(encoded[at:-6])
                struct.pack_into("<I", metadata, 4, len(metadata) - 8)
                struct.pack_into("<I", metadata, 12, 0)
                struct.pack_into("<I", metadata, 24, 0)
                return encoded[:at] + metadata
            at += size
        raise AssertionError("missing reference metadata block")
    if name.startswith("metadata-prefix"):
        at = 0
        while at < len(encoded):
            size = struct.unpack_from("<I", encoded, at + 4)[0] + 8
            frames = struct.unpack_from("<I", encoded, at + 20)[0]
            if frames == 0:
                assert at + size == len(encoded)
                return encoded[at:] + encoded[:at]
            at += size
        raise AssertionError("missing reference metadata block")
    if not name.startswith("tag-"):
        return encoded
    if "ape" in name:
        version = 1000 if name == "tag-apev1" else 2000
        item = struct.pack("<II", 4, 0) + b"Title\0Fern"
        flags = 0x80000000 if "header" in name else 0
        if flags:
            encoded += struct.pack(
                "<8sIIII8x", b"APETAGEX", version, len(item) + 32, 1, flags | 0x20000000
            )
        encoded += item + struct.pack(
            "<8sIIII8x", b"APETAGEX", version, len(item) + 32, 1, flags
        )
    if "id3v1" in name:
        encoded += b"TAGFern" + b"\0" * 121
    return encoded


def packed(data):
    return base64.b64encode(zlib.compress(data, 9)).decode()


def normalized(samples, bits, floating):
    padding = 0 if floating else ((bits + 7) // 8) * 8 - bits
    return struct.pack("<" + "i" * len(samples), *[x >> padding for x in samples])


def vector_source(vectors):
    lines = [
        "// Generated by scripts/gen-wavpack-vectors.py from WavPack 5.8.1.",
        "// Reference commit: " + PIN + ".",
        'import "wavpack_check" as corpus;',
        "",
        "pub const COUNT: i32 = " + str(len(vectors)) + ";",
        "",
        "pub function all(): corpus.Vector[] {",
        "  return [",
    ]
    for v in vectors:
        fields = [
            f"name: {json.dumps(v['name'])}",
            f'encoded: "{packed(v["encoded"])}"',
            f'pcm: "{packed(v["pcm"])}"',
            f"depth: {v['depth']}",
            f"channels: {v['channels']}",
            f"rate: {v['rate']}",
            f"frames: {v['frames']}",
            f"floating: {str(v['floating']).lower()}",
            f"mask: {v['mask']}u32",
        ]
        lines.append("    corpus.Vector { " + ", ".join(fields) + " },")
    lines += ["  ];", "}", ""]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    parser.add_argument(
        "--decoder", help="Optional Fern decoder executable for live PCM comparison"
    )
    args = parser.parse_args()
    vectors = []
    records = []
    ffmpeg_version = subprocess.check_output(
        ["ffmpeg", "-version"], text=True
    ).splitlines()[0]
    with tempfile.TemporaryDirectory(prefix="fern-wavpack-reference-") as work:
        for name, samples, channels, bits, flags, floating in cases():
            rate = 12345 if name == "short-24-2-19" else 44100
            encoded = tagged(
                encode(
                    samples,
                    channels,
                    bits,
                    rate,
                    flags,
                    block=16 if name.startswith("seek-") else 128,
                    floating=floating,
                    md5=name.startswith("metadata-"),
                ),
                name,
            )
            path = Path(work) / (name + ".wv")
            path.write_bytes(encoded)
            reference = verify(path, samples, channels)
            assert (
                reference["channels"] == channels
                and reference["depth"] == bits
                and reference["rate"] == rate
            )
            assert reference["mode"] & 2
            if name.startswith("tag-") and name != "tag-apev1":
                assert reference["title"] == "Fern", (name, reference)
            pcm = normalized(samples, bits, floating)
            fmt = "f32le" if floating else "s32le"
            result = subprocess.run(
                [
                    "ffmpeg",
                    "-v",
                    "error",
                    "-i",
                    str(path),
                    "-map",
                    "0:a:0",
                    "-c:a",
                    "pcm_" + fmt,
                    "-f",
                    fmt,
                    "pipe:1",
                ],
                capture_output=True,
            )
            ffmpeg_expected = pcm
            if not floating:
                logical = struct.unpack("<" + "i" * len(samples), pcm)
                ffmpeg_expected = struct.pack(
                    "<" + "I" * len(samples),
                    *[(x << (32 - bits)) & 0xFFFFFFFF for x in logical],
                )
            ffmpeg = (
                "exact"
                if not result.returncode and result.stdout == ffmpeg_expected
                else "different"
            )
            if args.decoder:
                target = Path(work) / (name + ".s32le")
                subprocess.run(
                    [args.decoder, str(path), str(target)],
                    check=True,
                    capture_output=True,
                )
                assert target.read_bytes() == pcm, name
            vector = dict(
                name=name,
                encoded=encoded,
                pcm=pcm,
                depth=bits,
                channels=channels,
                rate=rate,
                frames=len(samples) // channels,
                floating=floating,
                mask=reference["channel_mask"],
            )
            vectors.append(vector)
            records.append(
                {k: v for k, v in vector.items() if k not in ("encoded", "pcm")}
                | dict(
                    flags=flags,
                    encoded_sha256=hashlib.sha256(encoded).hexdigest(),
                    pcm_sha256=hashlib.sha256(pcm).hexdigest(),
                    reference="exact",
                    reference_tag_title=reference["title"],
                    reference_md5=reference["md5"],
                    ffmpeg=ffmpeg,
                    ffmpeg_sha256=hashlib.sha256(result.stdout).hexdigest(),
                )
            )
    smoke_names = {
        "short-16-1-7",
        "short-24-2-19",
        "short-float-2-6",
        "seek-24-2-35",
        "metadata-prefix-16",
        "metadata-tail-float",
        "metadata-tail-zero-total",
    }
    smoke = [v for v in vectors if v["name"] in smoke_names]
    metadata = dict(
        schema=1,
        reference="WavPack 5.8.1",
        reference_commit=PIN,
        ffmpeg=ffmpeg_version,
        pcm="Signed right-aligned i32 values, or unchanged IEEE 754 float bits",
        vectors=records,
    )
    outputs = {
        ROOT / "tests/stdlib/wavpack_vectors.fern": vector_source(vectors),
        ROOT / "tests/stdlib/wavpack_smoke_vectors.fern": vector_source(smoke),
        ROOT / "scripts/audio-reference/wavpack-vectors.json": json.dumps(
            metadata, indent=2
        )
        + "\n",
    }
    for path, text in outputs.items():
        if args.check:
            assert path.read_text() == text, "generated output differs: " + str(path)
        else:
            path.write_text(text)
    differences = [r["name"] for r in records if r["ffmpeg"] != "exact"]
    print(f"{len(vectors)} WavPack references verified, {len(smoke)} small controls")
    print("FFmpeg differences:", ", ".join(differences) or "none")


if __name__ == "__main__":
    main()
