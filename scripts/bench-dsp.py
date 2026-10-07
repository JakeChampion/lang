#!/usr/bin/env -S uv run --script
"""Measure native DSP callbacks and preserve independent oracle and sample evidence."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import time


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def checksum(values):
    result = 0
    for value in values:
        result = (result * 65599 + int(value * 4096) + 1048576) % 1000000007
    return result


def reference(block, blocks, delay):
    inputs = []
    for i in range(block):
        value = (i * 73 % 257 - 128) / 64
        if i % 31 == 0:
            value = 4.0
        elif i % 37 == 0:
            value = -4.0
        elif i % 11 == 0:
            value = 0.0
        inputs.append(value)
    ring = [0.0] * delay
    cursor = 0
    previous = previous2 = 0.0
    checks = 0
    for _ in range(blocks):
        outputs = []
        for value in inputs:
            gained = value * 1.5
            filtered = gained * 0.5 + previous * 0.25 + previous2 * 0.25
            previous2, previous = previous, gained
            wet = ring[cursor]
            ring[cursor] = filtered + wet * 0.25
            cursor = (cursor + 1) % delay
            outputs.append(max(-1.0, min(1.0, filtered * 0.75 + wet * 0.5)))
        final = checksum(outputs)
        checks = (checks + final) % 1000000007
    return dict(processed_samples=block * blocks, checksum=final, block_checks=checks,
                ring_checksum=checksum(ring), cursor=cursor)


def run(binary, variant, block, blocks, delay, sharing, prefix, expected):
    command = [str(binary), variant, str(block), str(blocks), str(delay), sharing, "samples"]
    started = time.monotonic_ns()
    with prefix.with_suffix(".stdout").open("w") as stdout, prefix.with_suffix(".stderr").open("w") as stderr:
        process = subprocess.Popen(command, stdout=stdout, stderr=stderr)
        _, status, usage = os.wait4(process.pid, 0)
        process.returncode = os.waitstatus_to_exitcode(status)
    elapsed = time.monotonic_ns() - started
    if process.returncode:
        raise RuntimeError(f"{command} exited {process.returncode}; see {prefix}.stderr")
    lines = prefix.with_suffix(".stdout").read_text().splitlines()
    report = json.loads(lines[-1])
    samples = []
    for i, line in enumerate(lines[:-1]):
        marker, index, duration = line.split(",")
        if marker != "sample" or int(index) != i or int(duration) < 0:
            raise RuntimeError(f"invalid sample: {line}")
        samples.append(int(duration))
    if len(samples) != blocks:
        raise RuntimeError("missing block samples")
    for key, wanted in dict(expected, variant=variant, block_size=block, blocks=blocks, delay_samples=delay, sharing=sharing).items():
        if report[key] != wanted:
            raise RuntimeError(f"{prefix}: {key}={report[key]}, expected {wanted}")
    ordered = sorted(samples)
    for key, per_mille in (("block_p50_ns", 500), ("block_p95_ns", 950), ("block_p99_ns", 990), ("block_p999_ns", 999), ("block_max_ns", 1000)):
        if report[key] != ordered[(len(ordered) * per_mille + 999) // 1000 - 1]:
            raise RuntimeError(f"percentile mismatch: {key}")
    if variant == "fip" and sharing == "unique" and (report["allocs"] != 0 or report["first_callback_allocs"] != 0):
        raise RuntimeError("unique FIP callback allocated after initialization")
    for key in ("startup_ns", "startup_allocs", "startup_fresh_bytes", "wall_ns", "allocs", "first_callback_allocs", "fresh_bytes"):
        if report[key] < 0:
            raise RuntimeError(f"negative metric: {key}")
    if report["wall_ns"] <= 0 or sum(samples) > report["wall_ns"] or report["first_callback_allocs"] > report["allocs"]:
        raise RuntimeError("invalid elapsed time or allocation totals")
    report.update(command=command, process_wall_ns=elapsed,
                  process_user_s=usage.ru_utime, process_system_s=usage.ru_stime,
                  process_peak_rss_bytes=usage.ru_maxrss * (1 if platform.system() == "Darwin" else 1024),
                  minor_faults=usage.ru_minflt, major_faults=usage.ru_majflt,
                  voluntary_switches=usage.ru_nvcsw, involuntary_switches=usage.ru_nivcsw,
                  callback_total_ns=sum(samples), samples_per_second=block * blocks * 1e9 / report["wall_ns"],
                  allocations_per_block=report["allocs"] / blocks, fresh_bytes_per_block=report["fresh_bytes"] / blocks)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compiler", type=Path, required=True)
    parser.add_argument("--block-sizes", type=int, nargs="+", default=[7])
    parser.add_argument("--blocks", type=int, default=5)
    parser.add_argument("--delay", type=int, default=31)
    parser.add_argument("--repeats", type=int, default=1)
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    if any(not 1 <= n <= 4096 for n in options.block_sizes) or not 1 <= options.blocks <= 1000000 or not 1 <= options.delay <= 8192 or options.repeats < 1:
        parser.error("block sizes1..4096, blocks1..1000000, delay1..8192 and positive repeats required")
    target = {("Darwin", "arm64"): "arm64-darwin", ("Linux", "aarch64"): "arm64-linux", ("Linux", "x86_64"): "x86-64-linux"}.get((platform.system(), platform.machine()))
    if target is None:
        parser.error("a supported native host is required")
    root = Path(__file__).resolve().parent.parent
    compiler = options.compiler.resolve(strict=True)
    options.output.mkdir(parents=True, exist_ok=False)
    output = options.output.resolve()
    source = output / "source"
    source.mkdir()
    inputs = sorted((root / "examples/fip").glob("dsp*.fern"))
    inputs += [Path(__file__).resolve(), root / "bootstrap/stage0.lock", root / "internal/stdlib/std/bench.fern"]
    hashes = {}
    for path in inputs:
        shutil.copy2(path, source / path.name)
        hashes[str(path.relative_to(root))] = digest(path)
    binary = output / "dsp"
    command = [str(compiler), "-target", target, "-o", str(binary), str(root / "examples/fip/dsp.fern"), str(root / "internal/stdlib")]
    with (output / "compile.log").open("w") as log:
        subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
    metadata = dict(host=platform.platform(), machine=platform.machine(), compiler=str(compiler), compiler_sha256=digest(compiler),
                    compile_command=command, binary_sha256=digest(binary), sources=hashes,
                    revision=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                    working_tree=subprocess.check_output(["git", "status", "--short"], cwd=root, text=True),
                    block_sizes=options.block_sizes, blocks=options.blocks, delay=options.delay, repeats=options.repeats,
                    measurement="No warm-up exemption. Samples time the callback; wall time also includes equal checksum traversals, validation and sample storage. Whole-process CPU/RSS/faults include startup, export and sorting. Fresh bytes are high-water growth, not requested bytes. Page faults are not cache misses.")
    (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    expected = {str(block): reference(block, options.blocks, options.delay) for block in options.block_sizes}
    (output / "oracle.json").write_text(json.dumps(expected, indent=2) + "\n")
    configurations = [(variant, sharing) for variant in ("direct", "fip") for sharing in ("unique", "shared")]
    with (output / "results.jsonl").open("w") as results:
        for repeat in range(options.repeats):
            offset = repeat % len(configurations)
            for block in options.block_sizes:
                for variant, sharing in configurations[offset:] + configurations[:offset]:
                    prefix = output / f"{repeat}-{block}-{variant}-{sharing}"
                    report = run(binary, variant, block, options.blocks, options.delay, sharing, prefix, expected[str(block)])
                    report["repeat"] = repeat
                    results.write(json.dumps(report) + "\n")
                    results.flush()
                    print(f"{prefix.name}: oracle and samples passed", flush=True)


if __name__ == "__main__":
    main()
