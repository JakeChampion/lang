#!/usr/bin/env python3
"""Check bounded AVC syntax controls with FFmpeg's independent SPS reader."""
import importlib.util
import os
from pathlib import Path
import shlex
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("avc_references", ROOT / "scripts/gen-video-avc-fixtures.py")
references = importlib.util.module_from_spec(spec)
spec.loader.exec_module(references)


class Writer:
    def __init__(self):
        self.bits = ""

    def u(self, value, width):
        assert 0 <= value < 1 << width
        self.bits += f"{value:0{width}b}"

    def ue(self, value):
        bits = f"{value + 1:b}"
        self.bits += "0" * (len(bits) - 1) + bits

    def se(self, value):
        self.ue(-2 * value if value <= 0 else 2 * value - 1)

    def finish(self, nal_header=0x67):
        self.bits += "1"
        self.bits += "0" * (-len(self.bits) % 8)
        raw = bytes(int(self.bits[i:i + 8], 2) for i in range(0, len(self.bits), 8))
        output = bytearray([nal_header])
        zeros = 0
        for byte in raw:
            if zeros == 2 and byte <= 3:
                output.append(3)
                zeros = 0
            output.append(byte)
            zeros = zeros + 1 if byte == 0 else 0
        return bytes(output)


def hrd(w, count):
    w.ue(count)
    w.u(255, 8)
    for _ in range(count + 1):
        w.ue(4294967294)
        w.ue(4294967294)
        w.u(1, 1)
    w.u(0xFFFFF, 20)


def vui(w, options):
    w.u(1, 1)
    ratio = options.get("ratio", 255)
    w.u(ratio, 8)
    if ratio == 255:
        w.u(options.get("sar_width", 1000), 16)
        w.u(1001, 16)
    w.u(1, 1)
    w.u(1, 1)
    w.u(1, 1)
    w.u(options.get("video_format", 5), 3)
    w.u(1, 1)
    w.u(1, 1)
    for identifier in (9, 16, 9):
        w.u(identifier, 8)
    w.u(1, 1)
    w.ue(options.get("chroma_location", 5))
    w.ue(4)
    w.u(1, 1)
    w.u(options.get("tick", 4294967295), 32)
    w.u(4294967294, 32)
    w.u(1, 1)
    for _ in range(2):
        w.u(1, 1)
        hrd(w, options.get("hrd_count", 31))
    w.u(0, 1)
    w.u(1, 1)
    w.u(1, 1)
    w.u(1, 1)
    for _ in range(4):
        w.ue(16)
    w.ue(options.get("reorder", 16))
    w.ue(options.get("buffering", 16))


def make(**options):
    w = Writer()
    profile = options.get("profile", 100)
    chroma = options.get("chroma", 1)
    w.u(profile, 8)
    w.u(options.get("constraints", 0), 8)
    w.u(40, 8)
    if options.get("overlong"):
        w.bits += "0" * 32
        return w.finish()
    w.ue(options.get("id", 0))
    if profile not in (66, 77, 88):
        w.ue(chroma)
        if chroma == 3:
            w.u(options.get("separate", 0), 1)
        w.ue(options.get("depth", 8) - 8)
        w.ue(0 if chroma == 0 else options.get("depth", 8) - 8)
        w.u(0, 1)
        scaling = options.get("scaling", False)
        w.u(int(scaling), 1)
        if scaling:
            for index in range(12 if chroma == 3 else 8):
                w.u(1, 1)
                delta = options.get("delta", -8)
                if delta == -8:
                    w.se(delta)
                else:
                    for j in range(16 if index < 6 else 64):
                        w.se(delta if j == 0 else 0)
    w.ue(options.get("frame_num", 0))
    poc = options.get("poc", 2)
    w.ue(poc)
    if poc == 0:
        w.ue(0)
    elif poc == 1:
        w.u(0, 1)
        w.se(-1)
        w.se(2)
        count = options.get("cycle", 255)
        w.ue(count)
        for _ in range(count):
            w.se(0)
    w.ue(options.get("references", 2))
    w.u(0, 1)
    w.ue(options.get("width", 3))
    w.ue(options.get("height", 1))
    only = options.get("only", 1)
    w.u(only, 1)
    if not only:
        w.u(options.get("adaptive", 0), 1)
    w.u(1, 1)
    w.u(1, 1)
    for value in options.get("crop", (1, 2, 1, 2)):
        w.ue(value)
    w.u(int(options.get("vui", False)), 1)
    if options.get("vui"):
        vui(w, options)
    return w.finish()


def controls():
    return [
        ("poc-cycle-255", make(profile=66, poc=1)),
        ("scaling-default-420", make(scaling=True)),
        ("scaling-dense-444", make(profile=244, chroma=3, scaling=True, delta=0)),
        ("separate-14-bit-fields", make(profile=244, chroma=3, separate=1, depth=14, only=0)),
        ("monochrome", make(chroma=0, depth=10)),
        ("vui-hrd-32-entry", make(vui=True, references=16)),
    ]


def invalid():
    return [(name, make(**({"hrd_count": 0, **options} if options.get("vui") else options)), category) for name, options, category in [
        ("unknown-profile", {"profile": 255}, "Unsupported"),
        ("reserved-constraint", {"constraints": 1}, "Header"),
        ("sequence-id", {"id": 32}, "Header"),
        ("chroma-format", {"chroma": 4}, "Header"),
        ("bit-depth", {"depth": 15}, "Header"),
        ("scaling-delta", {"scaling": True, "delta": 128}, "Header"),
        ("frame-number", {"frame_num": 13}, "Header"),
        ("picture-order", {"poc": 3}, "Header"),
        ("picture-order-cycle", {"poc": 1, "cycle": 256}, "Header"),
        ("reference-count", {"references": 17}, "Header"),
        ("integer-width", {"width": 4294967294}, "Limit"),
        ("empty-crop", {"crop": (32, 0, 0, 0)}, "Header"),
        ("overlong-code", {"overlong": True}, "Limit"),
        ("zero-aspect", {"vui": True, "sar_width": 0}, "Header"),
        ("reserved-aspect", {"vui": True, "ratio": 17}, "Header"),
        ("video-format", {"vui": True, "video_format": 7}, "Header"),
        ("chroma-location", {"vui": True, "chroma_location": 6}, "Header"),
        ("zero-timing", {"vui": True, "tick": 0}, "Header"),
        ("hrd-count", {"vui": True, "hrd_count": 32}, "Header"),
        ("reorder-count", {"vui": True, "reorder": 17}, "Header"),
        ("reorder-over-buffer", {"vui": True, "buffering": 15}, "Header"),
        ("references-over-buffer", {"vui": True, "buffering": 1, "reorder": 0}, "Header"),
    ]]


def build_trace():
    binary = ROOT / "build/video/avc-sps-trace"
    binary.parent.mkdir(parents=True, exist_ok=True)
    cflags = os.environ.get("FFMPEG_CFLAGS") or subprocess.check_output(["pkg-config", "--cflags", "libavcodec", "libavutil"], text=True)
    libs = os.environ.get("FFMPEG_LIBS") or subprocess.check_output(["pkg-config", "--libs", "libavcodec", "libavutil"], text=True)
    subprocess.run([os.environ.get("CC", "cc"), "-Wall", "-Wextra", "-Werror", *shlex.split(cflags),
                    str(ROOT / "scripts/video-reference/avc-trace.c"), *shlex.split(libs), "-o", str(binary)], check=True)
    return binary


def reference(binary, path):
    process = subprocess.run([str(binary), str(path)], check=True, capture_output=True, text=True)
    fields = references.trace_fields(process.stderr)
    assert fields["rbsp_stop_one_bit"] == 1
    return references.metadata(fields)


def main():
    binary = build_trace()
    lines = ["// Synthetic H.264 syntax controls, independently accepted by FFmpeg trace_headers.",
             'import "std/video/frame";',
             "pub struct Vector { name: string, bytes: string, values: i64[] }",
             "pub struct Invalid { name: string, bytes: string, error: frame.VideoError }",
             "pub function vectors(): Vector[] {", "  return ["]
    with tempfile.TemporaryDirectory() as temporary:
        for name, data in controls():
            path = Path(temporary) / (name + ".sps")
            path.write_bytes(data)
            values = reference(binary, path)
            lines.append(f'    Vector {{ name: "{name}", bytes: "{data.hex()}", values: [' + ", ".join(f"{n}i64" for n in values) + "] },")
    lines += ["  ];", "}", "pub function invalid(): Invalid[] {", "  return ["]
    for name, data, category in invalid():
        lines.append(f'    Invalid {{ name: "{name}", bytes: "{data.hex()}", error: frame.{category} }},')
    lines += ["  ];", "}"]
    target = ROOT / "tests/stdlib/video_avc_syntax_vectors.fern"
    target.write_text("\n".join(lines) + "\n")
    compiler = os.environ.get("FERN") or subprocess.check_output([str(ROOT / "bootstrap/bootstrap.sh"), "stage0"], text=True).strip()
    subprocess.run([compiler, "-fmt", "-w", str(target)], check=True)
    print("Wrote", target.relative_to(ROOT))


if __name__ == "__main__":
    main()
