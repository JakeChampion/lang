#!/usr/bin/env python3
"""Generate MP4 packet-index vectors with independent FFmpeg and FFprobe."""
import json
import os
import struct
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def run(*args):
    return subprocess.check_output(args)


def compact_tables(data):
    output = bytearray()
    at = 0
    while at < len(data):
        size, tag = struct.unpack_from(">I4s", data, at)
        assert 8 <= size <= len(data) - at
        body = data[at + 8:at + size]
        if tag in (b"moov", b"trak", b"mdia", b"minf", b"stbl"):
            body = compact_tables(body)
        elif tag == b"stco":
            count = struct.unpack_from(">I", body, 4)[0]
            values = struct.unpack_from(">" + "I" * count, body, 8)
            body = body[:8] + struct.pack(">" + "Q" * count, *values)
            tag = b"co64"
        elif tag == b"stsz":
            fixed, count = struct.unpack_from(">II", body, 4)
            assert fixed == 0
            values = struct.unpack_from(">" + "I" * count, body, 12)
            assert max(values) <= 65535
            body = body[:4] + struct.pack(">3xBI", 16, count) + struct.pack(">" + "H" * count, *values)
            tag = b"stz2"
        output += struct.pack(">I4s", len(body) + 8, tag) + body
        at += size
    return bytes(output)


def split_tables(data, offsets):
    output = bytearray()
    at = 0
    while at < len(data):
        size, tag = struct.unpack_from(">I4s", data, at)
        assert 8 <= size <= len(data) - at
        body = data[at + 8:at + size]
        if tag in (b"moov", b"trak", b"mdia", b"minf", b"stbl"):
            body = split_tables(body, offsets)
        elif tag in (b"stco", b"co64"):
            width = "Q" if tag == b"co64" else "I"
            body = body[:4] + struct.pack(">I", len(offsets)) + struct.pack(">" + width * len(offsets), *offsets)
        elif tag == b"stsc":
            # Each packet occupies one chunk with the existing single description.
            assert struct.unpack_from(">I", body, 16)[0] == 1
            body = body[:4] + struct.pack(">IIII", 1, 1, 1, 1)
        output += struct.pack(">I4s", len(body) + 8, tag) + body
        at += size
    return bytes(output)


def split_media(source, target):
    original = source.read_bytes()
    metadata = packets(source)
    assert len(metadata["streams"]) == 1
    samples = metadata["packets"]
    output = bytearray()
    movie = b""
    at = 0
    while at < len(original):
        size, tag = struct.unpack_from(">I4s", original, at)
        assert 8 <= size <= len(original) - at
        if tag == b"moov":
            movie = original[at:at + size]
        elif tag != b"mdat":
            output += original[at:at + size]
        at += size
    # Empty payloads and box headers are gaps, not part of neighboring payloads.
    output += struct.pack(">I4s", 8, b"mdat") * 65
    offsets = [0] * len(samples)
    for index in reversed(range(len(samples))):
        sample = samples[index]
        pos, size = int(sample["pos"]), int(sample["size"])
        output += struct.pack(">I4s", size + 8, b"mdat")
        offsets[index] = len(output)
        output += original[pos:pos + size]
        output += struct.pack(">I4s", 8, b"mdat")
    assert movie
    output += split_tables(movie, offsets)
    target.write_bytes(output)


def generate(directory):
    cases = []
    for name, encoder, options in (
        ("avc-bframes", "libx264", []),
        ("avc-signed-faststart", "libx264", ["-movflags", "+negative_cts_offsets+faststart"]),
        ("hevc-bframes", "libx265", ["-tag:v", "hvc1"]),
        ("avc-audio", "libx264", ["-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000"]),
    ):
        path = directory / (name + ".mp4")
        args = ["ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=32x32:rate=6"]
        if name == "avc-audio":
            args += options
        args += ["-t", "1", "-c:v", encoder, "-threads", "1", "-g", "4", "-bf", "2", "-pix_fmt", "yuv420p"]
        if encoder == "libx265":
            args += ["-x265-params", "pools=none:frame-threads=1:info=0:log-level=error"]
        if name == "avc-audio":
            args += ["-c:a", "aac", "-b:a", "32k"]
        else:
            args += options
        run(*args, str(path))
        cases.append((name, path))
    duplicate = directory / "avc-two-tracks.mp4"
    run("ffmpeg", "-v", "error", "-y", "-i", str(cases[0][1]), "-map", "0:v", "-map", "0:v", "-c", "copy", str(duplicate))
    cases.append(("avc-two-tracks", duplicate))
    compact = directory / "avc-compact-co64.mp4"
    original = cases[0][1].read_bytes()
    # Only the final moov changes size. Media offsets remain valid.
    assert original.rfind(b"moov") > original.find(b"mdat")
    compact.write_bytes(compact_tables(original))
    cases.append(("avc-compact-co64", compact))
    for name, source in (("avc-many-mdat", compact), ("hevc-many-mdat", cases[2][1])):
        target = directory / (name + ".mp4")
        split_media(source, target)
        cases.append((name, target))
    return cases


def packets(path):
    metadata = json.loads(run("ffprobe", "-v", "error", "-ignore_editlist", "1", "-select_streams", "v",
                              "-show_packets", "-show_streams", "-of", "json", str(path)))
    return metadata


def main():
    lines = ["// Generated by scripts/gen-video-mp4-fixtures.py with FFmpeg and FFprobe.",
             "pub struct Packet { offset: i64, bytes: i32, dts: i64, pts: i64, duration: u32, sync: boolean }",
             "pub struct Track { scale: u32, packets: Packet[] }",
             "pub struct Vector { name: string, bytes: string, hevc: boolean, tracks: Track[] }",
             "pub function vectors(): Vector[] {", "  return ["]
    with tempfile.TemporaryDirectory() as directory:
        for name, path in generate(Path(directory)):
            metadata = packets(path)
            lines.append('    Vector { name: "' + name + '", bytes: "' + path.read_bytes().hex() + '", hevc: ' + str(name.startswith("hevc")).lower() + ', tracks: [')
            for stream in metadata["streams"]:
                assert stream["width"] == 32 and stream["height"] == 32, stream
                numerator, denominator = map(int, stream["time_base"].split("/"))
                assert numerator == 1
                group = [p for p in metadata["packets"] if p["stream_index"] == stream["index"]]
                assert len(group) == 6
                lines.append(f'      Track {{ scale: {denominator}u32, packets: [')
                for p in group:
                    lines.append(f'        Packet {{ offset: {p["pos"]}i64, bytes: {p["size"]}, dts: {p["dts"]}i64, pts: {p["pts"]}i64, duration: {p["duration"]}u32, sync: {str("K" in p["flags"]).lower()} }},')
                lines.append("      ] },")
            lines.append("    ] },")
    lines += ["  ];", "}"]
    target = ROOT / "tests/stdlib/video_mp4_vectors.fern"
    target.write_text("\n".join(lines) + "\n")
    compiler = os.environ.get("FERN") or run(str(ROOT / "bootstrap/bootstrap.sh"), "stage0").decode().strip()
    subprocess.run([compiler, "-fmt", "-w", str(target)], check=True)
    print(run("ffmpeg", "-version").decode().splitlines()[0])
    print("Wrote", target.relative_to(ROOT))


if __name__ == "__main__":
    main()
