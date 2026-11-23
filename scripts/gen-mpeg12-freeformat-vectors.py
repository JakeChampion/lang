#!/usr/bin/env python3
"""Freeze free-format MPEG Layer I and II streams with independent scalar PCM."""
import argparse
import hashlib
import math
from pathlib import Path
import runpy
import struct
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
MP2 = runpy.run_path(str(ROOT / "scripts/gen-mp2-vectors.py"))
MP1 = runpy.run_path(str(ROOT / "scripts/gen-mp1-vectors.py"))


def crc(bits):
    value = 65535
    for bit in bits:
        feedback = (value >> 15) ^ int(bit)
        value = ((value << 1) & 65535) ^ (0x8005 if feedback else 0)
    return value


def layer2_protected(frame, kbps):
    """Count the allocation and scalefactor-selection bits as minimp3 reads them."""
    a, b, c = frame[1], frame[2], frame[3]
    mode, ext = c >> 6, c >> 4 & 3
    if a & 8:
        per_channel = kbps if mode == 3 else kbps // 2
        assert per_channel >= 56
        groups, bands = [(4, 3), (4, 8), (3, 12), (2, 7)], 27
        if per_channel >= 96 and (b >> 2 & 3) != 1:
            bands = 30
    else:
        groups, bands = [(4, 4), (3, 7), (2, 19)], 30
    stereo = 0 if mode == 3 else 4 * (ext + 1) if mode == 1 else 32
    stereo = min(stereo, bands)
    bits = "".join(format(v, "08b") for v in frame[6:])
    at, band, active = 0, 0, 0
    for width, count in groups:
        for _ in range(count):
            if band == bands:
                break
            left = int(bits[at:at + width], 2)
            at += width
            right = 0
            if band < stereo:
                right = int(bits[at:at + width], 2)
                at += width
            elif mode != 3:
                right = left
            active += (left != 0) + (right != 0)
            band += 1
    return bits[:at + 2 * active]


def tone(rate, channels, count):
    return b"".join(struct.pack("<f", 0.4 * math.sin(2 * math.pi * (317 + channel * 173) * i / rate)
                                + 0.2 * math.sin(2 * math.pi * (4111 + channel * 997) * i / rate))
                    for i in range(count) for channel in range(channels))


def relabel(data):
    """Clear the bitrate index of every Layer II frame and recompute protected CRCs."""
    out = bytearray()
    for at, size, _, _, kbps, _, protected in MP2["frame_offsets"](data):
        frame = bytearray(data[at:at + size])
        if protected:
            covered = layer2_protected(frame, kbps)
            header = "".join(format(v, "08b") for v in frame[2:4])
            assert crc(header + covered) == struct.unpack(">H", frame[4:6])[0]
        frame[2] &= 15
        if protected:
            header = "".join(format(v, "08b") for v in frame[2:4])
            frame[4:6] = struct.pack(">H", crc(header + covered))
        out += frame
    return bytes(out)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--minimp3", type=Path, required=True)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    header = args.minimp3.read_bytes()
    assert hashlib.sha256(header).hexdigest() == MP2["HEADER_SHA"]
    rows = []
    with tempfile.TemporaryDirectory() as directory:
        work = Path(directory)
        (work / "minimp3.h").write_bytes(header)
        oracle = work / "oracle"
        subprocess.run(["cc", "-O2", "-ffp-contract=off", "-I", work,
                        ROOT / "scripts/audio-reference/mp2.c", "-o", oracle], check=True)

        def reference(data, layer):
            stream, pcm = work / "input.mpa", work / "out.pcm"
            stream.write_bytes(data)
            subprocess.run([oracle, stream, pcm] + (["layer1"] if layer == 1 else []), check=True)
            return pcm.read_bytes()

        def row(layer, rate, channels, frames, data, pcm):
            assert len(pcm) == frames * (384 if layer == 1 else 1152) * channels * 4
            rows.append(f'    Vector {{ layer: {layer}, rate: {rate}, channels: {channels}, frames: {frames}, '
                        f'encoded: "{MP2["packed"](data)}", pcm: "{MP2["packed"](pcm)}" }}')

        # Layer I: the frame generator protects the first and last frames and pads the middle frame.
        for version, rate, mode, ext, dense, slots in [(1, 0, 3, 0, False, 101), (1, 1, 0, 0, True, 150),
                                                       (2, 0, 1, 0, False, 64), (2, 2, 1, 2, True, 90)]:
            data = b"".join(MP1["frame"](version, rate, mode, ext, seq, dense, 0, slots) for seq in range(3))
            row(1, [44100, 48000, 32000][rate] // version, 1 if mode == 3 else 2, 3, data, reference(data, 1))

        # Layer II: the table that free format selects matches the indexed stream at 192 kbit/s per
        # channel for MPEG-1, and at every bitrate for MPEG-2, so both streams must decode identically.
        for rate, channels, kbps, mode, protect in [(44100, 1, 192, 3, 0), (48000, 2, 384, 0, 1),
                                                    (32000, 2, 384, 1, 1), (24000, 2, 160, 1, 1),
                                                    (16000, 1, 64, 3, 0)]:
            source, encoded = work / "input.f32", work / "indexed.mp2"
            source.write_bytes(tone(rate, channels, 3 * 1152))
            subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "f32le", "-ar", str(rate), "-ac", str(channels),
                            "-i", str(source), "-c:a", "libtwolame", "-b:a", str(kbps * 1000), "-mode", str(mode),
                            "-error_protection", str(protect), "-f", "mp2", str(encoded)], check=True)
            indexed = encoded.read_bytes()
            frames = len(MP2["frame_offsets"](indexed))
            assert frames >= 3
            data = relabel(indexed)
            pcm = reference(data, 2)
            assert pcm == reference(indexed, 2)
            row(2, rate, channels, frames, data, pcm)
    text = ("// Generated by scripts/gen-mpeg12-freeformat-vectors.py using pinned scalar minimp3.\n"
            "pub struct Vector { layer: i32, rate: i32, channels: i32, frames: i32, encoded: string, pcm: string }\n\n"
            "pub function vectors(): Vector[] {\n  return [\n" + ",\n".join(rows) + ",\n  ];\n}\n")
    output = ROOT / "tests/stdlib/mpeg12_freeformat_vectors.fern"
    if args.check:
        if text != output.read_text():
            raise SystemExit("free-format Layer I and II references differ")
    else:
        output.write_text(text)


if __name__ == "__main__":
    main()
