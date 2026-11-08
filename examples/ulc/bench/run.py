#!/usr/bin/env python3
"""Reproducible end-to-end ULC benchmark against the upstream C tools."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import random
import statistics
import struct
import subprocess
import tempfile
import time
import wave


def run(args):
    return subprocess.run([str(a) for a in args], check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, timeout=120)


def fixture(path, kind, seconds):
    rng = random.Random(417)
    values = []
    rate = 44100
    for i in range(round(rate * seconds)):
        for ch in range(2):
            tone = 0.25 * math.sin(2 * math.pi * (440 + ch * 220) * i / rate)
            if kind == "silence":
                value = 0.0
            elif kind == "tone":
                value = tone + 0.1 * math.sin(i * 0.0031)
            elif kind == "noise":
                value = rng.uniform(-0.5, 0.5)
            else:
                value = tone
                if i % 10000 < 500:
                    value += rng.uniform(-0.4, 0.4)
            values.append(round(value * 32767))
    with wave.open(str(path), "wb") as wav:
        wav.setparams((2, 2, rate, 0, "NONE", "not compressed"))
        wav.writeframes(struct.pack(f"<{len(values)}h", *values))
    return hashlib.sha256(path.read_bytes()).hexdigest()


def samples(path):
    data = path.read_bytes()
    at = 12
    while at + 8 <= len(data):
        tag, size = struct.unpack_from("<4sI", data, at)
        if tag == b"data":
            return struct.unpack(f"<{size // 4}f", data[at + 8:at + 8 + size])
        at += 8 + size + size % 2
    raise ValueError("WAV data missing")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fern", type=Path, required=True)
    parser.add_argument("--reference", type=Path, required=True, help="C release directory")
    parser.add_argument("--compiler", type=Path, help="Fern compiler used for the executable")
    parser.add_argument("--reference-label", default="C default")
    parser.add_argument("--seconds", type=float, default=10)
    parser.add_argument("--repetitions", type=int, default=7)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--cpu", type=int)
    parser.add_argument("--cpus", help="comma-separated CPU affinity for both executables")
    parser.add_argument("--threads", type=int, choices=(1, 2, 4), default=1)
    args = parser.parse_args()
    if args.seconds <= 0 or args.repetitions < 3:
        parser.error("use positive seconds and at least three repetitions")
    if args.cpu is not None and args.cpus is not None:
        parser.error("select --cpu or --cpus")
    if args.cpu is not None:
        os.sched_setaffinity(0, {args.cpu})
    if args.cpus is not None:
        os.sched_setaffinity(0, {int(cpu) for cpu in args.cpus.split(",")})
    os.environ["ULC_THREADS"] = str(args.threads)
    fern = args.fern.resolve()
    reference = args.reference.resolve()
    cpu = "unknown"
    if Path("/proc/cpuinfo").exists():
        cpu = next((line.split(":", 1)[1].strip() for line in Path("/proc/cpuinfo").read_text().splitlines()
                    if line.startswith("model name")), "unknown")
    report = {"threads": args.threads, "platform": platform.platform(), "cpu": cpu,
              "affinity": sorted(os.sched_getaffinity(0)) if hasattr(os, "sched_getaffinity") else None,
              "seconds": args.seconds, "repetitions": args.repetitions,
              "reference_label": args.reference_label, "quality": 75, "block_size": 2048,
              "fern_binary_sha256": hashlib.sha256(fern.read_bytes()).hexdigest(),
              "reference_encoder_sha256": hashlib.sha256((reference / "ulcencodetool").read_bytes()).hexdigest(),
              "results": []}
    if args.compiler:
        compiler = args.compiler.resolve()
        report["compiler_version"] = run([compiler, "-version"]).stdout.decode().strip()
        report["compiler_sha256"] = hashlib.sha256(compiler.read_bytes()).hexdigest()
    with tempfile.TemporaryDirectory(prefix="ulc-benchmark-") as temp:
        root = Path(temp)
        for kind in ("silence", "tone", "noise", "transients"):
            source = root / "input.wav"
            checksum = fixture(source, kind, args.seconds)
            encoded = {name: root / f"{name}.ulc" for name in ("C", "Fern")}
            commands = {"C": [reference / "ulcencodetool", source, encoded["C"], "-75"],
                        "Fern": [fern, "encode", source, encoded["Fern"], "75", "2048"]}
            for command in commands.values():
                run(command)
            sizes = {name: path.stat().st_size for name, path in encoded.items()}
            for direction in ("encode", "decode"):
                outputs = {name: root / f"{name}.wav" for name in commands}
                if direction == "decode":
                    commands = {"C": [reference / "ulcdecodetool", encoded["C"], outputs["C"], "-format:FLOAT32"],
                                "Fern": [fern, "decode", encoded["C"], outputs["Fern"], "FLOAT32"]}
                elapsed = {name: [] for name in commands}
                # Alternate process order to reduce cache and frequency bias.
                for repetition in range(args.repetitions):
                    for name in (("C", "Fern") if repetition % 2 == 0 else ("Fern", "C")):
                        before = time.perf_counter_ns()
                        run(commands[name])
                        elapsed[name].append((time.perf_counter_ns() - before) / 1e9)
                medians = {name: statistics.median(values) for name, values in elapsed.items()}
                result = {"fixture": kind, "input_sha256": checksum, "direction": direction,
                          "seconds": elapsed, "median_seconds": medians,
                          "fern_speed_ratio": medians["C"] / medians["Fern"],
                          "encoded_bytes": sizes}
                if direction == "decode":
                    a, b = samples(outputs["C"]), samples(outputs["Fern"])
                    assert len(a) == len(b)
                    error = max((abs(x - y) for x, y in zip(a, b)), default=0)
                    assert error < 0.00002, error
                    result["max_sample_error"] = error
                report["results"].append(result)
                print(f"{kind:10} {direction:6} C={medians['C']:.4f}s Fern={medians['Fern']:.4f}s "
                      f"C/Fern={result['fern_speed_ratio']:.3f}", flush=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
