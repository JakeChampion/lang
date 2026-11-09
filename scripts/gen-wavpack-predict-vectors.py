#!/usr/bin/env python3
"""Freeze direct WavPack reference kernel results for decorrelation controls."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import random
import shutil
import struct
import subprocess
import sys
import tempfile
import urllib.request
import zlib

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts/audio-reference"))
from wavpack_reference import encode, verify

PIN = "4827b9889665b937b6ed71b9c6c0123152cd7a02"
SOURCES = {
    "unpack.c": (
        "src",
        "bba495a5c33f432d8acd82a36a52d79d0d2ff7d05a1a5bc6e110ab7a92a9391c",
    ),
    "decorr_utils.c": (
        "src",
        "3dc93ee065ed9459dfc534614112dcf420c0aa59d222b743157128f4df166110",
    ),
    "entropy_utils.c": (
        "src",
        "3d750a714829230d09a09701943c40ac10d8d3a8e4145fe9ca353205a18320d1",
    ),
    "wavpack_local.h": (
        "src",
        "1acd5f7f5ddeeb098899694124087dced63612d309f6108cf632df11ad609ad5",
    ),
    "wavpack.h": (
        "include",
        "3c61d65511e258c5dc120a6daaee626d6a1051f3254ca9b67ac654a8f6212a97",
    ),
}


def cases():
    rng = random.Random(14802)
    cases = []
    for channels in [1, 2]:
        for term in [17, 18]:
            for delta in [0, 2, 7]:
                for profile, frames in enumerate([0, 1, 17, 129]):
                    cases.append(make_case(rng, channels, term, delta, profile, frames))
        for term in (
            [1, 2, 3, 4, 5, 6, 7, 8]
            if channels == 1
            else [-3, -2, -1, 1, 2, 3, 4, 5, 6, 7, 8]
        ):
            cases.append(make_case(rng, channels, term, 7, 3, 17))
    for direction in [1, -1]:
        case = make_case(rng, 1, 17, 7, 2, 4800)
        case.update(
            name=f"adaptive-weight-{'positive' if direction == 1 else 'negative'}",
            weights=[127 if direction == 1 else 128],
            adaptive=256 | (512 if direction == -1 else 0),
            samples=[],
        )
        cases.append(case)
    return cases


def make_case(rng, channels, term, delta, profile, frames):
    count = channels * 2 if term > 8 else 2 if term < 0 else channels * term
    patterns = [[0], [8191, -8192], [4096, -4096, 1, -1], [-8192, 8191, 7936, -7936]]
    logs = [patterns[profile][i % len(patterns[profile])] for i in range(count)]
    weights = [[0, 0], [127, 128], [128, 127], [85, 171]][profile][:channels]
    values = [0, 1, -1, 2147483647, -2147483648, 1073741824, -1073741824]
    samples = [
        rng.randrange(-2147483648, 2147483648)
        if i % 3 == 2
        else values[i % len(values)]
        for i in range(frames * channels)
    ]
    return {
        "name": f"ch{channels}-term{term}-delta{delta}-n{frames}",
        "channels": channels,
        "frames": frames,
        "term": (term + 5) | delta << 5,
        "weights": weights,
        "history": list(struct.pack("<" + "h" * count, *logs)),
        "samples": samples,
    }


def wire(case):
    weights = case["weights"] + [0]
    return (
        struct.pack(
            "<5i",
            case["channels"],
            case["frames"],
            case["term"] | case.get("adaptive", 0),
            weights[0],
            weights[1],
        )
        + bytes(case["history"])
        + struct.pack("<" + "i" * len(case["samples"]), *case["samples"])
    )


def array(values, suffix=""):
    return "[" + ", ".join(str(value) + suffix for value in values) + "]"


def metadata_entries(encoded):
    at = 32
    entries = []
    while at < len(encoded):
        start = at
        tag, words = encoded[at : at + 2]
        at += 2
        if tag & 128:
            words |= encoded[at] << 8 | encoded[at + 1] << 16
            at += 2
        stop = at + words * 2
        entries.append((tag & 63, encoded[start:stop]))
        at = stop
    return entries


def file_control():
    original = encode([0], 1, 24, 48000)
    header = bytearray(original[:32])
    replacements = {2: bytes([22]), 3: bytes([127]), 4: struct.pack("<hh", 8191, -8192)}
    entries = []
    for ident, raw in metadata_entries(original):
        if ident == 47:
            continue
        if ident in replacements:
            content = replacements[ident]
            raw = bytes(
                [ident | (64 if len(content) % 2 else 0), (len(content) + 1) // 2]
            )
            raw += content + (b"\0" if len(content) % 2 else b"")
        entries.append(raw)
    flags = struct.unpack_from("<I", header, 24)[0]
    struct.pack_into("<I", header, 24, (flags & ~(31 << 18)) | 23 << 18)
    struct.pack_into("<I", header, 28, 0xFF7FFFFD)
    body = b"".join(entries)
    struct.pack_into("<I", header, 4, len(header) + len(body) + 6 - 8)
    encoded = bytes(header) + body
    checksum = 0xFFFFFFFF
    for (word,) in struct.iter_unpack("<H", encoded):
        checksum = (checksum * 3 + word) & 0xFFFFFFFF
    return encoded + bytes([47, 2]) + struct.pack("<I", checksum)


def compressed(data):
    return base64.b64encode(zlib.compress(data)).decode()


def fern(cases, encoded):
    lines = [
        "// Generated by scripts/gen-wavpack-predict-vectors.py with WavPack 5.8.1.",
        "// Reference commit: " + PIN + ".",
        'import "wavpack_predict_check";',
        'import "wavpack_check";',
        "",
        "pub function all(): wavpack_predict_check.Vector[] {",
        "  return [",
    ]
    for case in cases:
        lines.append(
            '    wavpack_predict_check.Vector { name: "'
            + case["name"]
            + '", channels: '
            + str(case["channels"])
            + ", frames: "
            + str(case["frames"])
            + ", term: "
            + str(case["term"])
            + "u8, weights: "
            + array(case["weights"], "u8")
            + ", history: "
            + array(case["history"], "u8")
            + ", samples: "
            + array(case["samples"])
            + ", expected: "
            + array(case["expected"])
            + " },"
        )
    lines += [
        "  ];",
        "}",
        "",
        "pub function file(): wavpack_check.Vector {",
        '  return wavpack_check.Vector { name: "large-predictor-history", encoded: "'
        + compressed(encoded)
        + '", pcm: "'
        + compressed(struct.pack("<i", -8388608))
        + '", depth: 24, channels: 1, rate: 48000, frames: 1, floating: false, mask: 4u32 };',
        "}",
        "",
    ]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    vectors = cases()
    with tempfile.TemporaryDirectory(prefix="wavpack-predict-reference-") as directory:
        temp = Path(directory)
        for name, (folder, digest) in SOURCES.items():
            data = (
                (args.source / name).read_bytes()
                if args.source
                else urllib.request.urlopen(
                    f"https://raw.githubusercontent.com/dbry/WavPack/{PIN}/{folder}/{name}",
                    timeout=30,
                ).read()
            )
            assert hashlib.sha256(data).hexdigest() == digest, name
            (temp / name).write_bytes(data)
        driver = ROOT / "scripts/audio-reference/wavpack_predict.c"
        shutil.copy(driver, temp / driver.name)
        binary = temp / "reference"
        subprocess.run(
            [
                "cc",
                "-O2",
                "-fwrapv",
                "-ffunction-sections",
                "-fdata-sections",
                "-Wl,--gc-sections",
                "-o",
                str(binary),
                str(temp / driver.name),
            ],
            check=True,
        )
        raw = subprocess.check_output(
            [str(binary)], input=b"".join(wire(case) for case in vectors)
        )
        at = 0
        for case in vectors:
            count = case["frames"] * case["channels"]
            if case.get("adaptive"):
                case["final_weight"] = struct.unpack_from("<i", raw, at)[0]
                assert not -32768 <= case["final_weight"] <= 32767
                at += 4
                case["samples"] = list(struct.unpack_from("<" + "i" * count, raw, at))
                at += count * 4
            case["expected"] = list(struct.unpack_from("<" + "i" * count, raw, at))
            at += count * 4
        assert at == len(raw)
        encoded = file_control()
        path = temp / "large-history.wv"
        path.write_bytes(encoded)
        file_info = verify(path, [-8388608], 1)
    text = fern(vectors, encoded)
    path = ROOT / "tests/stdlib/wavpack_predict_vectors.fern"
    metadata = {
        "reference_commit": PIN,
        "sources": {name: digest for name, (_, digest) in SOURCES.items()},
        "cases": len(vectors),
        "adaptive_final_weights": {
            case["name"]: case["final_weight"]
            for case in vectors
            if case.get("adaptive")
        },
        "reference_output_sha256": hashlib.sha256(raw).hexdigest(),
        "pcm_sha256": hashlib.sha256(
            b"".join(
                struct.pack("<" + "i" * len(case["expected"]), *case["expected"])
                for case in vectors
            )
        ).hexdigest(),
        "fixture_sha256": hashlib.sha256(text.encode()).hexdigest(),
        "file_control": {
            "sha256": hashlib.sha256(encoded).hexdigest(),
            "bytes": len(encoded),
            "reference_info": file_info,
        },
        "c_compiler": subprocess.check_output(
            ["cc", "--version"], text=True
        ).splitlines()[0],
    }
    meta = ROOT / "scripts/audio-reference/wavpack-predict-vectors.json"
    meta_text = json.dumps(metadata, indent=2) + "\n"
    if args.check:
        assert path.read_text() == text, "kernel vectors differ"
        frozen = json.loads(meta.read_text())
        assert {k: v for k, v in frozen.items() if k != "c_compiler"} == {
            k: v for k, v in metadata.items() if k != "c_compiler"
        }, "kernel metadata differs"
    else:
        path.write_text(text)
        meta.write_text(meta_text)
    print(f"{len(vectors)} pinned reference kernel controls")


if __name__ == "__main__":
    main()
