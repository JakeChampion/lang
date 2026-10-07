#!/usr/bin/env -S uv run --script
"""Measure native storage simulation waves against an independent object model."""
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


def reference(depth, waves, mode):
    disk = [[(page + 1) * 10000 + word for word in range(8)] for page in range(depth)]
    frames = [dict(page=-1, dirty=False, pin=False, age=0, data=[0] * 8) for _ in range(depth)]

    def disk_sum():
        return sum((p * 8 + w + 1) * value for p, row in enumerate(disk) for w, value in enumerate(row))

    def data_sum():
        return disk_sum() + sum((i * 8 + w + 1) * value for i, f in enumerate(frames) for w, value in enumerate(f["data"])) + sum((i + 1) * (f["page"] * 13 + int(f["dirty"])) for i, f in enumerate(frames))

    tick = cursor = errors = checksum = snapshots = latency = max_latency = 0
    for wave in range(waves):
        snapshots += data_sum()
        kind = (1, 2, 0, 3)[wave % 4]
        delay = 17 if mode == "delayed" else 0
        start = tick
        requests = [dict(phase="queued", frame=-1, due=0, code=0, value=0) for _ in range(depth)]
        remaining = depth
        while remaining:
            tick += 1
            page = cursor
            cursor = (cursor + 1) % depth
            request = requests[page]
            if request["phase"] == "queued":
                matches = [i for i, f in enumerate(frames) if f["page"] == page]
                frame = matches[0] if matches else -1
                code = 0
                if frame < 0:
                    if kind >= 2:
                        code = 12
                    else:
                        candidates = sorted((f["page"] >= 0, f["age"] if f["page"] >= 0 else 0, i)
                                            for i, f in enumerate(frames) if not f["pin"] and not f["dirty"])
                        if candidates:
                            frame = candidates[0][2]
                        else:
                            code = 11
                if not code and frames[frame]["pin"]:
                    code = 10
                if not code and kind == 3 and frames[frame]["dirty"]:
                    code = 13
                if code:
                    request.update(phase="done", code=code)
                else:
                    frames[frame]["pin"] = True
                    request.update(phase="waiting", frame=frame, due=tick + delay)
            elif request["phase"] == "waiting" and tick >= request["due"]:
                frame = frames[request["frame"]]
                frame["pin"] = False
                if mode == "faulted" and (wave * depth + page) % 11 == 0:
                    request["code"] = 14
                else:
                    if kind < 2 and frame["page"] != page:
                        frame.update(page=page, data=disk[page].copy(), dirty=False)
                    offset = wave // 4 % 8
                    if kind == 0:
                        request["value"] = frame["data"][offset]
                    elif kind == 1:
                        request["value"] = (wave + 1) * 17 + page - 1000000
                        frame["data"][offset] = request["value"]
                        frame["dirty"] = True
                    elif kind == 2:
                        if frame["dirty"]:
                            disk[page] = frame["data"].copy()
                        frame["dirty"] = False
                    else:
                        frame["page"] = -1
                    frame["age"] = 0 if kind == 3 else tick
                request["phase"] = "done"
            else:
                continue
            if request["phase"] == "done":
                remaining -= 1
                latency += tick - start
                # Collected after the whole wave, so a done slot stays occupied.
                request["phase"] = "retained"
        max_latency = max(max_latency, tick - start)
        errors += sum(r["code"] != 0 for r in requests)
        checksum += sum((page + 1) * (r["code"] * 1000003 + r["value"]) for page, r in enumerate(requests))
    return dict(depth=depth, waves=waves, accepted=depth * waves, completed=depth * waves,
                refusals=waves, errors=errors, checksum=checksum, snapshot_checks=snapshots,
                disk_checksum=disk_sum(), data_checksum=data_sum(), logical_ticks=tick,
                logical_latency_sum=latency, logical_latency_max=max_latency, scheduling_work=tick)


def run(binary, depth, waves, mode, sharing, prefix, expected):
    command = [str(binary), str(depth), str(waves), mode, sharing, "samples"]
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
        marker, number, duration = line.split(",")
        if marker != "sample" or int(number) != index or int(duration) < 0:
            raise RuntimeError(f"bad sample: {line}")
        samples.append(int(duration))
    if len(samples) != waves:
        raise RuntimeError("missing samples")
    for key, value in dict(expected, mode=mode, sharing=sharing).items():
        if report[key] != value:
            raise RuntimeError(f"{prefix}: {key}={report[key]}, expected {value}")
    ordered = sorted(samples)
    for key, percentile in (("wave_p50_ns", 500), ("wave_p95_ns", 950), ("wave_p99_ns", 990), ("wave_p999_ns", 999), ("wave_max_ns", 1000)):
        if report[key] != ordered[(len(ordered) * percentile + 999) // 1000 - 1]:
            raise RuntimeError(f"incorrect percentile: {key}")
    for key in ("startup_ns", "startup_allocs", "startup_fresh_bytes", "wall_ns", "allocs", "first_wave_allocs", "fresh_bytes"):
        if report[key] < 0:
            raise RuntimeError(f"negative {key}")
    if report["wall_ns"] <= 0 or sum(samples) > report["wall_ns"] or report["first_wave_allocs"] > report["allocs"]:
        raise RuntimeError("inconsistent timing or allocation totals")
    if sharing == "unique" and (report["allocs"] or report["first_wave_allocs"]):
        raise RuntimeError("unique state allocated")
    report.update(command=command, process_wall_ns=elapsed, process_user_s=usage.ru_utime,
                  process_system_s=usage.ru_stime, process_peak_rss_bytes=usage.ru_maxrss * (1 if platform.system() == "Darwin" else 1024),
                  minor_faults=usage.ru_minflt, major_faults=usage.ru_majflt,
                  voluntary_switches=usage.ru_nvcsw, involuntary_switches=usage.ru_nivcsw,
                  operation_total_ns=sum(samples), operations_per_second=expected["completed"] * 1e9 / report["wall_ns"],
                  allocations_per_operation=report["allocs"] / expected["completed"],
                  fresh_bytes_per_operation=report["fresh_bytes"] / expected["completed"],
                  logical_latency_mean=expected["logical_latency_sum"] / expected["completed"])
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compiler", type=Path, required=True)
    parser.add_argument("--depths", type=int, nargs="+", default=[1, 3])
    parser.add_argument("--waves", type=int, default=5)
    parser.add_argument("--repeats", type=int, default=1)
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    if any(not 1 <= n <= 128 for n in options.depths) or not 1 <= options.waves <= 10000 or options.repeats < 1:
        parser.error("depth1..128, waves1..10000 and positive repeats required")
    target = {("Darwin", "arm64"): "arm64-darwin", ("Linux", "aarch64"): "arm64-linux", ("Linux", "x86_64"): "x86-64-linux"}.get((platform.system(), platform.machine()))
    if target is None:
        parser.error("a supported native host is required")
    root = Path(__file__).resolve().parent.parent
    compiler = options.compiler.resolve(strict=True)
    options.output.mkdir(parents=True, exist_ok=False)
    output = options.output.resolve()
    source = output / "source"
    source.mkdir()
    hashes = {}
    inputs = sorted((root / "examples/fip").glob("storage*.fern"))
    inputs += [Path(__file__).resolve(), root / "bootstrap/stage0.lock", root / "internal/stdlib/std/bench.fern"]
    for path in inputs:
        shutil.copy2(path, source / path.name)
        hashes[str(path.relative_to(root))] = digest(path)
    binary = output / "storage"
    command = [str(compiler), "-target", target, "-o", str(binary), str(root / "examples/fip/storage.fern"), str(root / "internal/stdlib")]
    with (output / "compile.log").open("w") as log:
        subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
    metadata = dict(host=platform.platform(), machine=platform.machine(), compiler=str(compiler), compiler_sha256=digest(compiler),
                    compile_command=command, binary_sha256=digest(binary), sources=hashes,
                    revision=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                    working_tree=subprocess.check_output(["git", "status", "--short"], cwd=root, text=True),
                    depths=options.depths, waves=options.waves, repeats=options.repeats,
                    measurement="No warmup exemption. Samples cover full waves, including saturation refusal and completion collection. Wall time adds equal snapshot checksum work and sample storage. Export/sorting follow marks. Process CPU/RSS/faults include startup/export/sorting. Fresh bytes are high-water growth, not requested bytes. Logical latency is simulator ticks, not physical I/O time. Page faults are not cache misses.")
    (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    expected = {f"{depth}-{mode}": reference(depth, options.waves, mode) for depth in options.depths for mode in ("plain", "delayed", "faulted")}
    (output / "oracle.json").write_text(json.dumps(expected, indent=2) + "\n")
    configurations = [(mode, sharing) for mode in ("plain", "delayed", "faulted") for sharing in ("unique", "shared")]
    with (output / "results.jsonl").open("w") as results:
        for repeat in range(options.repeats):
            offset = repeat % len(configurations)
            for depth in options.depths:
                for mode, sharing in configurations[offset:] + configurations[:offset]:
                    prefix = output / f"{repeat}-{depth}-{mode}-{sharing}"
                    report = run(binary, depth, options.waves, mode, sharing, prefix, expected[f"{depth}-{mode}"])
                    report["repeat"] = repeat
                    results.write(json.dumps(report) + "\n")
                    results.flush()
                    print(f"{prefix.name}: oracle and samples passed", flush=True)


if __name__ == "__main__":
    main()
