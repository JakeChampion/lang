#!/usr/bin/env -S uv run --script
"""Measure native broker turnover and refusals, preserving oracle and sample evidence."""
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


def reference(subscribers, occupancy, turns, mode):
    def total(first, count):
        return 17 * (2 * first + count - 1) * count // 2 - 1000000 * count
    flowing = mode == "flow"
    return dict(observed_checksum=total(1, turns) if flowing else -999983 * turns,
                queued_checksum=total(turns + 1 if flowing else 1, occupancy),
                serial=occupancy + (turns if flowing else 1),
                deliveries=turns * subscribers if flowing else 0,
                refusals=0 if flowing else turns, free_slots=0,
                producer_slot=-1 if flowing else occupancy)


def run(binary, subscribers, occupancy, turns, mode, sharing, prefix):
    command = [str(binary), str(subscribers), str(occupancy), str(turns), mode, sharing, "samples"]
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
    if len(samples) != turns:
        raise RuntimeError("missing turn samples")
    expected = dict(reference(subscribers, occupancy, turns, mode), subscribers=subscribers,
                    occupancy=occupancy, turns=turns, mode=mode, sharing=sharing)
    for key, wanted in expected.items():
        if report[key] != wanted:
            raise RuntimeError(f"{prefix}: {key}={report[key]}, expected {wanted}")
    ordered = sorted(samples)
    for key, p in (("turn_p50_ns", 500), ("turn_p95_ns", 950), ("turn_p99_ns", 990), ("turn_p999_ns", 999), ("turn_max_ns", 1000)):
        if report[key] != ordered[(len(ordered) * p + 999) // 1000 - 1]:
            raise RuntimeError(f"percentile mismatch: {key}")
    if report["fill_allocs"] != 0 or sharing == "unique" and report["allocs"] != 0:
        raise RuntimeError("unique broker lifecycle allocated after initialization")
    for key in ("startup_ns", "startup_allocs", "startup_fresh_bytes", "wall_ns", "allocs", "first_turn_allocs", "fresh_bytes"):
        if report[key] < 0:
            raise RuntimeError(f"negative metric: {key}")
    if report["wall_ns"] <= 0 or sum(samples) > report["wall_ns"] or report["first_turn_allocs"] > report["allocs"]:
        raise RuntimeError("invalid elapsed time or allocation totals")
    report.update(command=command, process_wall_ns=elapsed,
                  process_user_s=usage.ru_utime, process_system_s=usage.ru_stime,
                  process_peak_rss_bytes=usage.ru_maxrss * (1 if platform.system() == "Darwin" else 1024),
                  minor_faults=usage.ru_minflt, major_faults=usage.ru_majflt,
                  voluntary_switches=usage.ru_nvcsw, involuntary_switches=usage.ru_nivcsw,
                  operation_total_ns=sum(samples), turns_per_second=turns * 1e9 / report["wall_ns"],
                  deliveries_per_second=report["deliveries"] * 1e9 / report["wall_ns"],
                  refusals_per_second=report["refusals"] * 1e9 / report["wall_ns"],
                  allocations_per_turn=report["allocs"] / turns, fresh_bytes_per_turn=report["fresh_bytes"] / turns)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compiler", type=Path, required=True)
    parser.add_argument("--subscribers", type=int, nargs="+", default=[1, 4])
    parser.add_argument("--occupancies", type=int, nargs="+", default=[1, 8])
    parser.add_argument("--turns", type=int, default=5)
    parser.add_argument("--repeats", type=int, default=1)
    parser.add_argument("--modes", nargs="+", choices=["flow", "full"], default=["flow", "full"])
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    if any(not 1 <= n <= 16 for n in options.subscribers) or any(not 1 <= n <= 4096 for n in options.occupancies) or not 1 <= options.turns <= 1000000 or options.repeats < 1:
        parser.error("subscribers1..16, occupancy1..4096, turns1..1000000 and positive repeats required")
    if "full" in options.modes and 4096 in options.occupancies:
        parser.error("full-queue mode needs one extra producer slot, so occupancy must be <=4095")
    target = {("Darwin", "arm64"): "arm64-darwin", ("Linux", "aarch64"): "arm64-linux", ("Linux", "x86_64"): "x86-64-linux"}.get((platform.system(), platform.machine()))
    if target is None:
        parser.error("a supported native host is required")
    root = Path(__file__).resolve().parent.parent
    compiler = options.compiler.resolve(strict=True)
    options.output.mkdir(parents=True, exist_ok=False)
    output = options.output.resolve()
    source = output / "source"
    source.mkdir()
    inputs = sorted((root / "examples/fip").glob("broker*.fern"))
    inputs += [Path(__file__).resolve(), root / "bootstrap/stage0.lock", root / "internal/stdlib/std/bench.fern"]
    hashes = {}
    for path in inputs:
        shutil.copy2(path, source / path.name)
        hashes[str(path.relative_to(root))] = digest(path)
    binary = output / "broker"
    command = [str(compiler), "-target", target, "-o", str(binary), str(root / "examples/fip/broker.fern"), str(root / "internal/stdlib")]
    with (output / "compile.log").open("w") as log:
        subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
    metadata = dict(host=platform.platform(), machine=platform.machine(), compiler=str(compiler), compiler_sha256=digest(compiler),
                    compile_command=command, binary_sha256=digest(binary), sources=hashes,
                    revision=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                    working_tree=subprocess.check_output(["git", "status", "--short"], cwd=root, text=True),
                    subscribers=options.subscribers, occupancies=options.occupancies, turns=options.turns, repeats=options.repeats, modes=options.modes,
                    measurement="Fill allocations are separate. Samples time one turnover with delivery validation, or one full-queue refusal. Wall time also includes equal external-alias checks and sample storage. Export/sorting follow marks. Whole-process CPU/RSS/faults include startup, fill, export and sorting. Fresh bytes are high-water growth, not requested bytes. Page faults are not cache misses.")
    (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    expected = {f"{subscribers}-{occupancy}-{mode}": reference(subscribers, occupancy, options.turns, mode)
                for subscribers in options.subscribers for occupancy in options.occupancies for mode in options.modes}
    (output / "oracle.json").write_text(json.dumps(expected, indent=2) + "\n")
    configurations = [(mode, sharing) for mode in options.modes for sharing in ("unique", "shared")]
    with (output / "results.jsonl").open("w") as results:
        for repeat in range(options.repeats):
            offset = repeat % len(configurations)
            for subscribers in options.subscribers:
                for occupancy in options.occupancies:
                    for mode, sharing in configurations[offset:] + configurations[:offset]:
                        prefix = output / f"{repeat}-{subscribers}-{occupancy}-{mode}-{sharing}"
                        report = run(binary, subscribers, occupancy, options.turns, mode, sharing, prefix)
                        report["repeat"] = repeat
                        results.write(json.dumps(report) + "\n")
                        results.flush()
                        print(f"{prefix.name}: oracle and samples passed", flush=True)


if __name__ == "__main__":
    main()
