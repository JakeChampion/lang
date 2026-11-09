#!/usr/bin/env python3
"""Generate independent IVF packet references with FFmpeg and FFprobe."""
import json
import os
from pathlib import Path
import struct
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def generate(directory):
    cases = []
    for name, encoder in (("VP8", "libvpx"), ("VP9", "libvpx-vp9"), ("AV1", "libaom-av1")):
        path = directory / f"{name.lower()}.ivf"
        subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i",
                        "testsrc2=size=32x32:rate=6", "-frames:v", "6", "-c:v", encoder,
                        "-threads", "1", "-cpu-used", "8", "-g", "4", "-pix_fmt", "yuv420p",
                        "-f", "ivf", str(path)], check=True)
        cases.append((name, path))
    return cases


def reference(path):
    return json.loads(subprocess.check_output(["ffprobe", "-v", "error", "-show_packets",
                                             "-show_streams", "-of", "json", str(path)]))


def main():
    lines = ["// Generated with FFmpeg and FFprobe by scripts/gen-video-ivf-fixtures.py.",
             "pub struct Packet { offset: i64, timestamp: u64, bytes: string }",
             "pub struct Vector { codec: string, bytes: string, packets: Packet[] }",
             "pub function vectors(): Vector[] {", "  return ["]
    with tempfile.TemporaryDirectory() as directory:
        for codec, path in generate(Path(directory)):
            data = path.read_bytes()
            packets = reference(path)["packets"]
            lines.append(f'    Vector {{ codec: "{codec}", bytes: "{data.hex()}", packets: [')
            at = 32
            for packet in packets:
                size, timestamp = struct.unpack_from("<IQ", data, at)
                assert size == int(packet["size"]) and timestamp == int(packet["pts"])
                assert at == int(packet["pos"])
                payload = data[at + 12:at + 12 + size]
                lines.append(f'      Packet {{ offset: {at + 12}i64, timestamp: {timestamp}u64, bytes: "{payload.hex()}" }},')
                at += 12 + size
            assert at == len(data)
            lines.append("    ] },")
    lines += ["  ];", "}"]
    target = ROOT / "tests/stdlib/video_ivf_vectors.fern"
    target.write_text("\n".join(lines) + "\n")
    compiler = os.environ.get("FERN") or subprocess.check_output([str(ROOT / "bootstrap/bootstrap.sh"), "stage0"], text=True).strip()
    subprocess.run([compiler, "-fmt", "-w", str(target)], check=True)
    print(subprocess.check_output(["ffmpeg", "-version"], text=True).splitlines()[0])
    print("Wrote", target.relative_to(ROOT))


if __name__ == "__main__":
    main()
