#!/usr/bin/env -S uv run --script
"""Measure a native primary-compiler ETL binary; retain samples and oracle checks."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import shutil
import subprocess
import time


def reference(count):
    result = dict(seen=count, accepted=0, invalid=0, filtered=0, refused=0,
                  total=0, low=16501, high=-1, checksum=0)
    for i in range(count):
        sensor = 255 if i % 19 == 0 else i % 16
        quality = 7 if i % 23 == 0 else i % 4
        value = i * 173 % 20000 - 5000
        if sensor >= 16 or quality >= 4 or not -4000 <= value <= 12500:
            result["invalid"] += 1
        elif quality < 2:
            result["filtered"] += 1
        else:
            normalized = value + 4000
            result["accepted"] += 1
            result["total"] += normalized
            result["low"] = min(result["low"], normalized)
            result["high"] = max(result["high"], normalized)
            result["checksum"] += sum(normalized.to_bytes(4, "little") + i.to_bytes(4, "little"))
    return result


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(binary, variant, count, batch, prefix):
    command = [str(binary), variant, str(count), str(batch), "samples"]
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
    if len(samples) != (count - 1) // batch + 1:
        raise RuntimeError("missing batch samples")
    ordered = sorted(samples)
    for field, fraction in [("batch_p50_ns", .5), ("batch_p95_ns", .95),
                            ("batch_p99_ns", .99), ("batch_p999_ns", .999)]:
        if report[field] != ordered[math.ceil(len(ordered) * fraction) - 1]:
            raise RuntimeError(f"percentile mismatch: {field}")
    if report["batch_max_ns"] != max(samples):
        raise RuntimeError("maximum mismatch")
    if variant in ("fip", "fbip") and report["steady_allocs"] != 0:
        raise RuntimeError("steady-state allocation")
    report.update(command=command, process_wall_ns=elapsed,
                  process_user_s=usage.ru_utime, process_system_s=usage.ru_stime,
                  process_peak_rss_bytes=usage.ru_maxrss * (1 if platform.system() == "Darwin" else 1024),
                  minor_faults=usage.ru_minflt, major_faults=usage.ru_majflt,
                  voluntary_switches=usage.ru_nvcsw, involuntary_switches=usage.ru_nivcsw,
                  records_per_second=count * 1e9 / report["wall_ns"],
                  allocations_per_record=report["steady_allocs"] / count,
                  fresh_bytes_per_record=report["steady_fresh_bytes"] / count)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--records", type=int, default=4096)
    parser.add_argument("--batches", type=int, nargs="+", default=[1, 64, 1024])
    parser.add_argument("--repeats", type=int, default=5)
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    if not 0 < options.records <= 268435455 or options.repeats < 1 or any(not 0 < b <= 268435455 for b in options.batches):
        parser.error("positive counts and byte lengths fitting i32 are required")
    if platform.system() not in ("Darwin", "Linux"):
        parser.error("native Darwin or Linux measurement required")
    root = Path(__file__).resolve().parent.parent
    binary = options.binary.resolve(strict=True)
    options.output.mkdir(parents=True, exist_ok=False)
    source = options.output / "source"
    source.mkdir()
    inputs = [root / "examples/fip" / name for name in ("etl.fern", "etl_core.fern", "etl_variants.fern")]
    inputs += [Path(__file__).resolve(), root / "bootstrap/stage0.lock"]
    hashes = {}
    for path in inputs:
        shutil.copy2(path, source / path.name)
        hashes[str(path.relative_to(root))] = digest(path)
    metadata = dict(host=platform.platform(), machine=platform.machine(),
                    binary=str(binary), binary_sha256=digest(binary), sources=hashes,
                    revision=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                    working_tree=subprocess.check_output(["git", "status", "--short"], cwd=root, text=True),
                    records=options.records, batches=options.batches, repeats=options.repeats,
                    measurement="wall_ns includes batch processing and output consumption; batch samples exclude output consumption. Process CPU/RSS/faults include startup, sample export and report sorting. Fresh bytes are allocator high-water growth, not total requested bytes. Faults are not hardware cache misses.")
    (options.output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    expected = reference(options.records)
    (options.output / "oracle.json").write_text(json.dumps(expected, indent=2) + "\n")
    variants = ["baseline", "batched", "fbip", "fip"]
    with (options.output / "results.jsonl").open("w") as results:
        for repeat in range(options.repeats):
            # Rotate order deterministically to distribute thermal/order effects.
            order = variants[repeat % 4:] + variants[:repeat % 4]
            for batch in options.batches:
                for variant in order:
                    prefix = options.output / f"{repeat}-{batch}-{variant}"
                    report = run(binary, variant, options.records, batch, prefix)
                    for key, value in expected.items():
                        if report[key] != value:
                            raise RuntimeError(f"{prefix}: {key}={report[key]}, expected {value}")
                    report["repeat"] = repeat
                    results.write(json.dumps(report) + "\n")
                    results.flush()
                    print(f"{repeat} batch={batch} {variant}: oracle and samples passed", flush=True)


if __name__ == "__main__":
    main()
