#!/usr/bin/env python3
"""Native CLI tests, plus optional cross-decoding against the upstream C tools.

Run `make -C examples/ulc test`. Set ULC_REFERENCE_DIR to the upstream release directory
for the interoperability suite. No third-party Python packages are required.
"""
import math
import os
from pathlib import Path
import random
import struct
import subprocess
import tempfile
import wave

ROOT = Path(__file__).resolve().parents[1]
ULC = Path(os.environ.get("ULC", ROOT / "build/ulc")).resolve()
REFERENCE = os.environ.get("ULC_REFERENCE_DIR")


def run(*args, good=True):
    result = subprocess.run([str(a) for a in args], capture_output=True, timeout=60)
    if good:
        assert result.returncode == 0, (args, result.stdout, result.stderr)
    else:
        assert result.returncode == 1, (args, result.returncode, result.stderr)
    return result


def float_samples(path):
    data = path.read_bytes()
    at = 12
    while at + 8 <= len(data):
        tag, size = struct.unpack_from("<4sI", data, at)
        if tag == b"data":
            return struct.unpack(f"<{size // 4}f", data[at + 8:at + 8 + size])
        at += 8 + size + size % 2
    raise AssertionError("no WAV data")


def compare(a, b, tolerance=0.00001):
    a, b = float_samples(a), float_samples(b)
    assert len(a) == len(b), (len(a), len(b))
    error = max((abs(x - y) for x, y in zip(a, b)), default=0)
    assert error < tolerance, error
    return error


def pack(codes):
    return bytes(codes[i] | ((codes[i + 1] if i + 1 < len(codes) else 0) << 4)
                 for i in range(0, len(codes), 2))


def container(blocks, size=256, channels=2, offset=24):
    return (struct.pack("<4sHHIIHHI", b"ULC2", size, max(map(len, blocks), default=0),
                        len(blocks), 44100, channels, 0, offset)
            + b"\0" * (offset - 24) + b"".join(blocks))


PATTERNS = [0, 0x8, 0x19, 0x91, 0x12A, 0x1A2, 0x2A1, 0xA21,
            0x123B, 0x12B3, 0x13B2, 0x1B32, 0x23B1, 0x2B31, 0x3B21, 0xB321]


def synthetic():
    blocks = []
    # Every window shape and overlap scale, switching sizes in both directions.
    for cycle in range(2):
        for pattern in range(1, 16):
            for scale in range(8):
                codes = [scale] if pattern == 1 else [8 | scale, pattern]
                for channel in range(2):
                    p = PATTERNS[pattern]
                    while p:
                        n = 256 >> (p & 7)
                        # Signed coefficients, extended precision, zero run,
                        # ordinary quantizer change, noise run, and noise tail.
                        codes += [3, 2, 14, 7, 9, 15, 14, 4, 6, 0, 3,
                                  15, 0, 8, 0, 0, 2]
                        if n >= 64:
                            codes += [1, 0, 0]  # 33 zeros
                        codes += [15, 15, 5, 8, 7]
                        p >>= 4
                blocks.append(pack(codes))
    # Silence resets the spectrum but must preserve the lapping history.
    blocks += [pack([0, 14, 15, 14, 15])] * 3
    return container(blocks, offset=32)


def main():
    with tempfile.TemporaryDirectory(prefix="fern-ulc-") as temp:
        p = Path(temp)
        rng = random.Random(9081)
        for channels in (1, 2, 3):
            source = p / f"input-{channels}.wav"
            with wave.open(str(source), "wb") as wav:
                wav.setparams((channels, 2, 44100, 0, "NONE", "not compressed"))
                samples = []
                for i in range(4097):
                    for ch in range(channels):
                        value = 0.3 * math.sin(i * (0.08 + ch * 0.05))
                        if i % 599 < 20:
                            value += rng.uniform(-0.5, 0.5)
                        samples.append(round(value * 32767))
                wav.writeframes(struct.pack(f"<{len(samples)}h", *samples))
            for size in (256, 2048):
                encoded = p / "encoded.ulc"
                run(ULC, "encode", source, encoded, 75, size)
                run(ULC, "info", encoded)
                fern_wav = p / "fern.wav"
                run(ULC, "decode", encoded, fern_wav, "FLOAT32")
                if REFERENCE:
                    run(Path(REFERENCE) / "ulcdecodetool", encoded, p / "c.wav", "-format:FLOAT32")
                    compare(fern_wav, p / "c.wav")
                    for quality in (10, 75):
                        run(Path(REFERENCE) / "ulcencodetool", source, encoded, f"-{quality}", f"-blocksize:{size}")
                        run(Path(REFERENCE) / "ulcdecodetool", encoded, p / "c.wav", "-format:FLOAT32")
                        run(ULC, "decode", encoded, fern_wav, "FLOAT32")
                        compare(fern_wav, p / "c.wav")
        # Exercise supported block size endpoints without quadratic transforms.
        for size in (512, 1024, 4096, 8192, 16384, 32768):
            run(ULC, "encode", source, p / "large.ulc", 10, size)
            run(ULC, "decode", p / "large.ulc", p / "large.wav")
        for fmt, width in (("PCM8", 1), ("PCM16", 2), ("PCM24", 3)):
            run(ULC, "decode", encoded, p / "pcm.wav", fmt)
            with wave.open(str(p / "pcm.wav"), "rb") as w:
                assert w.getsampwidth() == width
        print("ok - native CLI, all block sizes, mono/stereo/three-channel audio, WAV formats")
        bad = p / "bad.ulc"
        cases = [b"", b"ULC2", container([pack([0, 14, 15, 14, 15])])[:-1],
                 container([pack([8, 0])]), container([pack([8, 1])]),
                 container([pack([0, 14, 13])]), container([pack([0, 15])]),
                 container([pack([0, 0, 1, 15, 15])]),
                 container([], size=300), container([], channels=0)]
        for value in cases:
            bad.write_bytes(value)
            run(ULC, "decode", bad, p / "bad.wav", good=False)
        run(ULC, "encode", source, p / "bad.ulc", 0, good=False)
        run(ULC, "encode", source, p / "bad.ulc", 101, good=False)
        run(ULC, "encode", source, p / "bad.ulc", 50, 300, good=False)
        run(ULC, "decode", encoded, p / "bad.wav", "bad-format", good=False)
        run(ULC, "decode", p / "missing", p / "bad.wav", good=False)
        assert not (p / "bad.wav").exists()
        print("ok - malformed streams and invalid CLI arguments fail before writing output")
        synth = synthetic()
        (p / "synthetic.ulc").write_bytes(synth)
        run(ULC, "decode", p / "synthetic.ulc", p / "synthetic-fern.wav", "FLOAT32")
        if REFERENCE:
            run(Path(REFERENCE) / "ulcdecodetool", p / "synthetic.ulc", p / "synthetic-c.wav", "-format:FLOAT32")
            error = compare(p / "synthetic-fern.wav", p / "synthetic-c.wav")
            print(f"ok - C interoperability, every window shape and overlap scale (max sample error {error:.3g})")
        else:
            print("SKIP - C interoperability (set ULC_REFERENCE_DIR to enable)")


if __name__ == "__main__":
    main()
