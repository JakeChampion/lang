#!/usr/bin/env -S uv run --script
"""Run equivalent native simulation variants and retain samples and oracle evidence."""
import argparse
import bisect
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


def displacement(to, source):
    value = to - source
    if value >= 2048:
        value -= 4096
    elif value < -2048:
        value += 4096
    return value


def reference(count, ticks, clustered):
    rows = []
    for i in range(count):
        target = -1 if i % 3 == 0 else (i + 1) % count
        x, y = (i % 8, i // 8 % 8) if clustered else (i * 73 % 4096, i * 151 % 4096)
        rows.append([x, y, i * 7 % 9 - 4, i * 11 % 9 - 4, 100, i % 3, target, int(target >= 0), 0])
    checks, checksum = 0, 0
    for tick in range(ticks + 1):
        if tick:
            checks = (checks + checksum) % 1000000007
        moved = []
        for i, old in enumerate(rows):
            x, y, vx, vy, health, kind, target, mode, _ = old
            if (i + tick) % 17 == 0:
                target = -1
            elif (i + tick) % 11 == 0:
                target = (i + tick) % count
            vx = vy = 0
            if mode != 2:
                if target < 0:
                    vx, vy = (i * 7 + tick) % 9 - 4, (i * 11 + tick * 3) % 9 - 4
                else:
                    dx = displacement(rows[target][0], x)
                    dy = displacement(rows[target][1], y)
                    vx = (int(dx > 0) - int(dx < 0)) * (kind + 1)
                    vy = (int(dy > 0) - int(dy < 0)) * (kind + 1)
            moved.append([(x + vx) % 4096, (y + vy) % 4096, vx, vy, health, kind, target, mode, 0])
        # Independent coordinate sweep, not the subject's linked 16x16 grid.
        images = sorted((row[0] + shift, i) for i, row in enumerate(moved) for shift in (-4096, 0, 4096))
        xs = [item[0] for item in images]
        for i, row in enumerate(moved):
            x, y = row[:2]
            first, last = bisect.bisect_left(xs, x - 128), bisect.bisect_right(xs, x + 128)
            neighbors = 0
            for at in range(first, last):
                candidate_x, j = images[at]
                dy = displacement(moved[j][1], y)
                if i != j and (candidate_x - x) ** 2 + dy ** 2 <= 128 ** 2:
                    neighbors += 1
            row[8] = neighbors
        checksum = tick + 1
        for row in moved:
            x, y, vx, vy, health, kind, target, mode, neighbors = row
            health = min(100, health + 3) if mode == 2 else max(0, min(100, health - min(4, neighbors) + kind))
            mode = 2 if health < 25 or mode == 2 and health < 75 else int(target >= 0)
            row[4], row[7] = health, mode
            for value in (x, y, vx + 4, vy + 4, health, kind, target + 1, mode, neighbors):
                checksum = (checksum * 65599 + value) % 1000000007
        rows = moved
    return dict(checksum=checksum, snapshot_checks=checks, initial_tick=1, final_tick=ticks + 1)


def run(binary, variant, count, ticks, layout, sharing, prefix, expected):
    command = [str(binary), variant, str(count), str(ticks), layout, sharing, "samples"]
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
    if len(samples) != ticks:
        raise RuntimeError("missing tick samples")
    for key, wanted in dict(expected, variant=variant, entities=count, ticks=ticks, layout=layout, sharing=sharing).items():
        if report[key] != wanted:
            raise RuntimeError(f"{prefix}: {key}={report[key]}, expected {wanted}")
    ordered = sorted(samples)
    for key, per_mille in (("tick_p50_ns", 500), ("tick_p95_ns", 950), ("tick_p99_ns", 990), ("tick_p999_ns", 999), ("tick_max_ns", 1000)):
        if report[key] != ordered[(len(ordered) * per_mille + 999) // 1000 - 1]:
            raise RuntimeError(f"percentile mismatch: {key}")
    if not count * ticks <= report["candidates"] <= count * count * ticks:
        raise RuntimeError("candidate work exceeded its bound")
    if sharing == "unique" and variant != "persistent" and report["steady_allocs"] != 0:
        raise RuntimeError("unique bounded world allocated during measurement")
    for key in ("startup_ns", "startup_allocs", "startup_fresh_bytes", "wall_ns", "steady_allocs", "steady_fresh_bytes"):
        if report[key] < 0:
            raise RuntimeError(f"negative metric: {key}")
    if report["wall_ns"] <= 0 or sum(samples) > report["wall_ns"]:
        raise RuntimeError("invalid elapsed time")
    report.update(command=command, process_wall_ns=elapsed,
                  process_user_s=usage.ru_utime, process_system_s=usage.ru_stime,
                  process_peak_rss_bytes=usage.ru_maxrss * (1 if platform.system() == "Darwin" else 1024),
                  minor_faults=usage.ru_minflt, major_faults=usage.ru_majflt,
                  voluntary_switches=usage.ru_nvcsw, involuntary_switches=usage.ru_nivcsw,
                  ticks_per_second=ticks * 1e9 / report["wall_ns"],
                  allocations_per_tick=report["steady_allocs"] / ticks,
                  fresh_bytes_per_tick=report["steady_fresh_bytes"] / ticks)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compiler", type=Path, required=True)
    parser.add_argument("--entities", type=int, nargs="+", default=[33])
    parser.add_argument("--ticks", type=int, default=5)
    parser.add_argument("--repeats", type=int, default=1)
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    if any(not 0 <= n <= 4096 for n in options.entities) or not 1 <= options.ticks <= 999999 or options.repeats < 1:
        parser.error("entities0..4096, measured ticks1..999999 and positive repeats required")
    targets = {("Darwin", "arm64"): "arm64-darwin", ("Linux", "aarch64"): "arm64-linux", ("Linux", "x86_64"): "x86-64-linux"}
    target = targets.get((platform.system(), platform.machine()))
    if target is None:
        parser.error("a supported native host is required")
    root = Path(__file__).resolve().parent.parent
    compiler = options.compiler.resolve(strict=True)
    options.output.mkdir(parents=True, exist_ok=False)
    output = options.output.resolve()
    source = output / "source"
    source.mkdir()
    inputs = sorted((root / "examples/fip").glob("simulation*.fern"))
    inputs += [Path(__file__).resolve(), root / "bootstrap/stage0.lock", root / "internal/stdlib/std/bench.fern"]
    hashes = {}
    for path in inputs:
        shutil.copy2(path, source / path.name)
        hashes[str(path.relative_to(root))] = digest(path)
    binary = output / "simulation"
    command = [str(compiler), "-target", target, "-o", str(binary), str(root / "examples/fip/simulation.fern"), str(root / "internal/stdlib")]
    with (output / "compile.log").open("w") as log:
        subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
    metadata = dict(host=platform.platform(), machine=platform.machine(), compiler=str(compiler),
                    compiler_sha256=digest(compiler), compile_command=command, binary_sha256=digest(binary),
                    sources=hashes, revision=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                    working_tree=subprocess.check_output(["git", "status", "--short"], cwd=root, text=True),
                    entities=options.entities, ticks=options.ticks, repeats=options.repeats,
                    measurement="One warm-up tick is excluded. Tick samples include one additional checksum traversal in both sharing modes. Whole-process CPU/RSS/faults include startup, export and sorting. Fresh bytes are high-water growth, not total requested bytes. Page faults are not cache misses.")
    (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    expected = {}
    for count in options.entities:
        for layout in ("spread", "cluster"):
            expected[f"{count}-{layout}"] = reference(count, options.ticks, layout == "cluster")
    (output / "oracle.json").write_text(json.dumps(expected, indent=2) + "\n")
    configurations = [(variant, sharing) for variant in ("persistent", "fbip", "fip") for sharing in ("unique", "shared")]
    work = {}
    with (output / "results.jsonl").open("w") as results:
        for repeat in range(options.repeats):
            offset = repeat % len(configurations)
            for count in options.entities:
                for layout in ("spread", "cluster"):
                    for variant, sharing in configurations[offset:] + configurations[:offset]:
                        key = f"{count}-{layout}"
                        prefix = output / f"{repeat}-{key}-{variant}-{sharing}"
                        report = run(binary, variant, count, options.ticks, layout, sharing, prefix, expected[key])
                        work.setdefault(key, report["candidates"])
                        if report["candidates"] != work[key]:
                            raise RuntimeError("representations performed different candidate work")
                        report["repeat"] = repeat
                        results.write(json.dumps(report) + "\n")
                        results.flush()
                        print(f"{prefix.name}: oracle, work and samples passed", flush=True)


if __name__ == "__main__":
    main()
