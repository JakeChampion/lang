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
import re
import statistics
import struct
import subprocess
import tempfile
import time
import wave


def run(args):
    return subprocess.run([str(a) for a in args], check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, timeout=120)


def fixture(path, kind, seconds, channels=2, rate=44100):
    rng = random.Random(417)
    values = []
    for i in range(round(rate * seconds)):
        for ch in range(channels):
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
        wav.setparams((channels, 2, rate, 0, "NONE", "not compressed"))
        wav.writeframes(struct.pack(f"<{len(values)}h", *values))
    return hashlib.sha256(path.read_bytes()).hexdigest()


def samples(path, output_format="FLOAT32"):
    data = path.read_bytes()
    at = 12
    while at + 8 <= len(data):
        tag, size = struct.unpack_from("<4sI", data, at)
        if tag == b"data":
            payload = data[at + 8:at + 8 + size]
            if output_format == "PCM8":
                return tuple((value - 128) / 128 for value in payload)
            if output_format == "PCM16":
                return tuple(value / 32768 for value in struct.unpack(f"<{size // 2}h", payload))
            if output_format == "PCM24":
                return tuple(int.from_bytes(payload[i:i + 3], "little", signed=True) / 8388608
                             for i in range(0, size, 3))
            return struct.unpack(f"<{size // 4}f", payload)
        at += 8 + size + size % 2
    raise ValueError("WAV data missing")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fern", type=Path, required=True)
    parser.add_argument("--baseline-fern", type=Path, help="compare exact decode output with an earlier Fern binary")
    parser.add_argument("--baseline-source-commit", help="source revision of the earlier Fern binary")
    parser.add_argument("--reference", type=Path, required=True, help="C release directory")
    parser.add_argument("--compiler", type=Path, help="Fern compiler used for the executable")
    parser.add_argument("--oracle", type=Path, help="reference64 decoder for independent float64 accuracy checks")
    parser.add_argument("--reference-label", default="C default")
    parser.add_argument("--source-commit", help="Git commit used for the Fern compiler and codec")
    parser.add_argument("--seconds", type=float, default=10)
    parser.add_argument("--repetitions", type=int, default=7)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--cpu", type=int)
    parser.add_argument("--cpus", help="comma-separated CPU affinity for both executables")
    parser.add_argument("--threads", type=int, choices=(1, 2, 4), default=1)
    parser.add_argument("--block-size", type=int, choices=tuple(1 << n for n in range(8, 16)), default=2048)
    parser.add_argument("--channels", type=int, default=2)
    parser.add_argument("--sample-rate", type=int, default=44100)
    parser.add_argument("--quality", type=float, default=75)
    parser.add_argument("--rate-mode", choices=("vbr", "cbr", "abr"), default="vbr")
    parser.add_argument("--bitrate", type=float, default=96)
    parser.add_argument("--output-format", choices=("PCM8", "PCM16", "PCM24", "FLOAT32"), default="FLOAT32")
    parser.add_argument("--direction", choices=("encode", "decode", "both"), default="both")
    args = parser.parse_args()
    if args.baseline_fern and args.direction != "decode":
        parser.error("--baseline-fern requires --direction decode")
    if args.seconds <= 0 or args.repetitions < 3:
        parser.error("use positive seconds and at least three repetitions")
    if not 1 <= args.channels <= 255 or not 1 <= args.sample_rate <= 384000 or not 0 < args.quality <= 100:
        parser.error("use channels 1..255, sample rate 1..384000, and quality in (0, 100]")
    if not math.isfinite(args.bitrate) or args.bitrate <= 0:
        parser.error("use a finite positive bitrate")
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
              "reference_label": args.reference_label, "quality": args.quality if args.rate_mode == "vbr" else None,
              "rate_mode": args.rate_mode, "bitrate_kbps": args.bitrate if args.rate_mode != "vbr" else None,
              "block_size": args.block_size,
              "channels": args.channels, "sample_rate": args.sample_rate,
              "output_format": args.output_format, "direction": args.direction,
              "fern_binary_sha256": hashlib.sha256(fern.read_bytes()).hexdigest(),
              "reference_encoder_sha256": hashlib.sha256((reference / "ulcencodetool").read_bytes()).hexdigest(),
              "reference_decoder_sha256": hashlib.sha256((reference / "ulcdecodetool").read_bytes()).hexdigest(),
              "source_commit": args.source_commit,
              "benchmark_script_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              "results": []}
    if args.compiler:
        compiler = args.compiler.resolve()
        report["compiler_version"] = run([compiler, "-version"]).stdout.decode().strip()
        report["compiler_sha256"] = hashlib.sha256(compiler.read_bytes()).hexdigest()
    baseline = args.baseline_fern.resolve() if args.baseline_fern else None
    if baseline:
        report["baseline_binary_sha256"] = hashlib.sha256(baseline.read_bytes()).hexdigest()
        report["baseline_source_commit"] = args.baseline_source_commit
    oracle = args.oracle.resolve() if args.oracle else None
    if oracle:
        report["oracle_sha256"] = hashlib.sha256(oracle.read_bytes()).hexdigest()
    with tempfile.TemporaryDirectory(prefix="ulc-benchmark-") as temp:
        root = Path(temp)
        for kind in ("silence", "tone", "noise", "transients"):
            source = root / "input.wav"
            checksum = fixture(source, kind, args.seconds, args.channels, args.sample_rate)
            encoded = {name: root / f"{name}.ulc" for name in ("C", "Fern")}
            average = None
            fern_mode, fern_rate, c_rate = "encode", f"{args.quality:g}", f"-{args.quality:g}"
            if args.rate_mode != "vbr":
                fern_mode = "encode-" + args.rate_mode
                fern_rate = c_rate = f"{args.bitrate:g}"
            if args.rate_mode == "abr":
                analysis = run([reference / "ulcencodetool", source, encoded["C"], "-75", f"-blocksize:{args.block_size}"])
                match = re.search(r"Avg complexity = ([0-9.]+)", analysis.stdout.decode())
                if match is None:
                    raise ValueError("C complexity analysis is missing")
                average = float(match[1]) or 1.0
                if not 0 < average <= 1:
                    raise ValueError("C complexity is outside (0, 1]")
                c_rate += f",{average:.5f}"
            commands = {"C": [reference / "ulcencodetool", source, encoded["C"], c_rate, f"-blocksize:{args.block_size}"],
                        "Fern": [fern, fern_mode, source, encoded["Fern"], fern_rate, str(args.block_size)]}
            if average is not None:
                commands["Fern"].append(f"{average:.5f}")
            for command in commands.values():
                run(command)
            sizes = {name: path.stat().st_size for name, path in encoded.items()}
            directions = ("encode", "decode") if args.direction == "both" else (args.direction,)
            for direction in directions:
                outputs = {name: root / f"{name}.wav" for name in commands}
                if direction == "decode":
                    commands = {"C": [reference / "ulcdecodetool", encoded["C"], outputs["C"], f"-format:{args.output_format}"],
                                "Fern": [fern, "decode", encoded["C"], outputs["Fern"], args.output_format]}
                    if baseline:
                        outputs["Baseline"] = root / "Baseline.wav"
                        commands["Baseline"] = [baseline, "decode", encoded["C"], outputs["Baseline"], args.output_format]
                elapsed = {name: [] for name in commands}
                # Alternate process order to reduce cache and frequency bias.
                for repetition in range(args.repetitions):
                    for name in (tuple(commands) if repetition % 2 == 0 else tuple(reversed(commands))):
                        before = time.perf_counter_ns()
                        run(commands[name])
                        elapsed[name].append((time.perf_counter_ns() - before) / 1e9)
                medians = {name: statistics.median(values) for name, values in elapsed.items()}
                result = {"fixture": kind, "input_sha256": checksum, "direction": direction,
                          "seconds": elapsed, "median_seconds": medians,
                          "fern_speed_ratio": medians["C"] / medians["Fern"],
                          "encoded_bytes": sizes}
                if baseline:
                    assert outputs["Baseline"].read_bytes() == outputs["Fern"].read_bytes(), "Fern output differs from baseline"
                    result["final_wav_bytes_identical"] = True
                    result["baseline_speed_ratio"] = medians["Baseline"] / medians["Fern"]
                if average is not None:
                    result["average_complexity"] = average
                    result["complexity_source"] = "C VBR 75 analysis outside timing"
                if direction == "encode" and oracle:
                    with wave.open(str(source), "rb") as wav:
                        original = tuple(value / 32768 for value in struct.unpack(
                            f"<{wav.getnframes() * args.channels}h", wav.readframes(wav.getnframes())))
                    power = sum(value * value for value in original) / max(1, len(original))
                    expected_count = (math.ceil(len(original) / (args.channels * args.block_size)) + 2) * args.block_size * args.channels
                    for name in ("C", "Fern"):
                        run([oracle, encoded[name], outputs[name]])
                        decoded = samples(outputs[name])
                        assert len(decoded) == expected_count
                        assert all(math.isfinite(value) for value in decoded)
                        aligned = decoded[2 * args.block_size * args.channels:]
                        mse = sum((actual - expected) ** 2 for actual, expected in zip(aligned, original)) / max(1, len(original))
                        result[f"{name.lower()}_rms_error"] = math.sqrt(mse)
                        result[f"{name.lower()}_snr_db"] = 10 * math.log10(power / mse) if power > 0 and mse > 0 else None
                        result[f"{name.lower()}_file_kbps"] = sizes[name] * 8 / args.seconds / 1000
                    del original, decoded, aligned
                if direction == "decode":
                    a, b = samples(outputs["C"], args.output_format), samples(outputs["Fern"], args.output_format)
                    assert len(a) == len(b)
                    error = max((abs(x - y) for x, y in zip(a, b)), default=0)
                    result["max_sample_error"] = error
                    if oracle:
                        expected = root / "oracle.wav"
                        run([oracle, encoded["C"], expected])
                        truth = samples(expected)
                        assert len(truth) == len(b)
                        quantization_error = 0.0
                        if args.output_format != "FLOAT32":
                            scale = 1 << (int(args.output_format[3:]) - 1)
                            truth = tuple(max(-1.0, min(1.0 - 1 / scale, value)) for value in truth)
                            quantization_error = 0.5 / scale
                            result["oracle_reference"] = "float64 FFT clipped to PCM range"
                        for name, values in (("C", a), ("Fern", b)):
                            errors = [abs(x - y) for x, y in zip(values, truth)]
                            result[f"{name.lower()}_oracle_max_error"] = max(errors, default=0)
                            result[f"{name.lower()}_oracle_rms_error"] = (sum(x * x for x in errors) / max(1, len(errors))) ** 0.5
                        assert result["fern_oracle_max_error"] < 0.00001 + quantization_error, result["fern_oracle_max_error"]
                        del truth, errors
                    else:
                        tolerance = 0.00002
                        if args.output_format != "FLOAT32":
                            tolerance += 1 / (1 << (int(args.output_format[3:]) - 1))
                        assert error < tolerance, error
                    del a, b
                report["results"].append(result)
                print(f"{kind:10} {direction:6} C={medians['C']:.4f}s Fern={medians['Fern']:.4f}s "
                      f"C/Fern={result['fern_speed_ratio']:.3f}", flush=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
