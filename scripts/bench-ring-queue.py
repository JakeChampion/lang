#!/usr/bin/env -S uv run --script
"""Compare a generic ring, scalar inline ring and ordinary array FIFO."""
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


def reference(capacity, turns, mode):
    return dict(count=capacity,
                checksum=capacity * (capacity + 1) // 2 + (capacity * turns if mode == "flow" else 0),
                checks=turns * (turns + capacity if mode == "flow" else capacity + 1))


def run(binary, representation, capacity, turns, mode, sharing, prefix):
    command = [str(binary), str(capacity), str(turns), mode, sharing, "samples"]
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
    for index, line in enumerate(lines[:-1]):
        marker, position, duration = line.split(",")
        if marker != "sample" or int(position) != index or int(duration) < 0:
            raise RuntimeError(f"invalid sample: {line}")
        samples.append(int(duration))
    if len(samples) != turns:
        raise RuntimeError("missing samples")
    expected = dict(reference(capacity, turns, mode), capacity=capacity, turns=turns, mode=mode, sharing=sharing)
    for key, value in expected.items():
        if report[key] != value:
            raise RuntimeError(f"{prefix}: {key}={report[key]}, expected {value}")
    ordered = sorted(samples)
    for key, p in (("turn_p50_ns", 500), ("turn_p95_ns", 950), ("turn_p99_ns", 990), ("turn_p999_ns", 999), ("turn_max_ns", 1000)):
        if report[key] != ordered[(len(ordered) * p + 999) // 1000 - 1]:
            raise RuntimeError(f"percentile mismatch: {key}")
    for key in ("fill_allocs", "startup_ns", "startup_allocs", "startup_fresh_bytes", "wall_ns", "allocs", "first_turn_allocs", "fresh_bytes"):
        if report[key] < 0:
            raise RuntimeError(f"negative metric: {key}")
    if report["wall_ns"] <= 0 or sum(samples) > report["wall_ns"] or report["first_turn_allocs"] > report["allocs"]:
        raise RuntimeError("invalid elapsed time or allocation totals")
    if representation != "array" and (report["fill_allocs"] != 0 or sharing == "unique" and report["allocs"] != 0):
        raise RuntimeError("unique fixed ring allocated after construction")
    report.update(representation=representation, command=command, process_wall_ns=elapsed,
                  process_user_s=usage.ru_utime, process_system_s=usage.ru_stime,
                  process_peak_rss_bytes=usage.ru_maxrss * (1 if platform.system() == "Darwin" else 1024),
                  minor_faults=usage.ru_minflt, major_faults=usage.ru_majflt,
                  operation_total_ns=sum(samples), turns_per_second=turns * 1e9 / report["wall_ns"])
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compiler", type=Path, required=True)
    parser.add_argument("--capacities", type=int, nargs="+", default=[1, 8])
    parser.add_argument("--turns", type=int, default=5)
    parser.add_argument("--repeats", type=int, default=1)
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    if any(not 1 <= n <= 4096 for n in options.capacities) or not 1 <= options.turns <= 1000000 or options.repeats < 1:
        parser.error("capacity1..4096, turns1..1000000 and positive repeats required")
    target = {("Darwin", "arm64"): "arm64-darwin", ("Linux", "aarch64"): "arm64-linux", ("Linux", "x86_64"): "x86-64-linux"}.get((platform.system(), platform.machine()))
    if target is None:
        parser.error("a supported native host is required")
    root = Path(__file__).resolve().parent.parent
    compiler = options.compiler.resolve(strict=True)
    options.output.mkdir(parents=True, exist_ok=False)
    output = options.output.resolve()
    source = output / "source"
    source.mkdir()
    inputs = [root / "examples/fip" / name for name in ("ring_queue.fern", "bounded_ring.fern", "queue_inline.fern", "queue_array.fern")]
    inputs += [Path(__file__).resolve(), root / "bootstrap/stage0.lock", root / "internal/stdlib/std/bench.fern", root / "internal/stdlib/std/array.fern"]
    hashes = {}
    for path in inputs:
        shutil.copy2(path, source / path.name)
        hashes[str(path.relative_to(root))] = digest(path)
    builds, binaries = {}, {}
    for representation, module in (("ring", "bounded_ring"), ("inline", "queue_inline"), ("array", "queue_array")):
        driver = source / f"driver_{representation}.fern"
        text = (source / "ring_queue.fern").read_text()
        if representation != "ring":
            text = text.replace("bounded_ring", module).replace("Ring[i64]", "Ring")
        driver.write_text(text)
        binary = output / representation
        command = [str(compiler), "-target", target, "-o", str(binary), str(driver), str(root / "internal/stdlib")]
        with (output / f"compile-{representation}.log").open("w") as log:
            subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
        binaries[representation] = binary
        builds[representation] = dict(command=command, binary_sha256=digest(binary), driver_sha256=digest(driver))
    metadata = dict(host=platform.platform(), compiler=str(compiler), compiler_sha256=digest(compiler),
                    revision=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                    working_tree=subprocess.check_output(["git", "status", "--short"], cwd=root, text=True),
                    sources=hashes, builds=builds, capacities=options.capacities, turns=options.turns, repeats=options.repeats,
                    measurement="Scalar i64 payloads require no payload container allocation. Startup includes sample storage, queue construction and fill; fill allocations also reported separately. Samples time drop/push or full refusal. Shared retains the whole old queue and reads its endpoints after mutation; unique performs equal endpoint reads before mutation. Wall includes these reads, checks and sample storage. Final full FIFO verification, export and sorting follow marks. Whole-process CPU/RSS/faults include them all. Array drop copies the suffix using std/array; no claim of optimal array queue. Fresh bytes are high-water growth, not requested bytes; faults are not cache misses.")
    (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    (output / "oracle.json").write_text(json.dumps({f"{n}-{m}": reference(n, options.turns, m) for n in options.capacities for m in ("flow", "full")}, indent=2) + "\n")
    configurations = [(mode, sharing, representation) for mode in ("flow", "full") for sharing in ("unique", "shared") for representation in binaries]
    with (output / "results.jsonl").open("w") as results:
        for repeat in range(options.repeats):
            offset = repeat % len(configurations)
            for capacity in options.capacities:
                for mode, sharing, representation in configurations[offset:] + configurations[:offset]:
                    prefix = output / f"{repeat}-{capacity}-{mode}-{sharing}-{representation}"
                    report = run(binaries[representation], representation, capacity, options.turns, mode, sharing, prefix)
                    report["repeat"] = repeat
                    results.write(json.dumps(report) + "\n")
                    results.flush()
                    print(f"{prefix.name}: oracle and samples passed", flush=True)


if __name__ == "__main__":
    main()
