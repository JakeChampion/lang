#!/usr/bin/env -S uv run --script
"""Measure configurable KV cores against an independent byte-string dictionary."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import time

MOD = 2147483647


def fold(digest, data):
    for byte in data:
        digest = (digest * 1000003 + byte) % MOD
    return digest


def key_bytes(key, width):
    return bytes(key & 255 if j == 0 else key >> 8 if j == 1 else (key * 31 + j) & 255 for j in range(width))


def request(n, key_width, value_width, universe, mix):
    key = (n * 1103515245 + 12345) % MOD % universe
    phase = n * 37 % 100
    read, put, increment = {"read": (80, 10, 5), "write": (20, 45, 20), "mixed": (50, 25, 15)}[mix]
    op = 2 if phase < read else 1 if phase < read + put else 4 if phase < read + put + increment else 3
    return op, key_bytes(key, key_width), bytes((n * 17 + j * 13 + 7) & 255 for j in range(value_width))


def apply(entries, requests, capacity):
    out = bytearray()
    for op, key, value in requests:
        if op == 1:
            if key not in entries and len(entries) == capacity:
                out.append(3)
            else:
                entries[key] = value
                out.append(1)
        elif key not in entries:
            out.append(2)
        elif op == 3:
            del entries[key]
            out.append(1)
        else:
            if op == 4:
                current = entries[key]
                number = (int.from_bytes(current[:8], "little") + int.from_bytes(value[:8], "little")) % (1 << 64)
                entries[key] = number.to_bytes(8, "little") + current[8:]
            out.append(4)
            out.extend(entries[key])
    return bytes(out)


def reference(capacity, key_width, value_width, batch, turns, mix, load):
    universe = max(1, capacity * load // 100)
    tape = [request(n, key_width, value_width, universe, mix) for n in range(batch * 100)]
    counts = {op: sum(req[0] == op for req in tape) for op in range(1, 5)}
    entries = {}
    seed = [(1, key_bytes(n, key_width), bytes((n * 17 + j * 13 + 7) & 255 for j in range(value_width))) for n in range(min(capacity, universe) // 2)]
    output = b""
    for at in range(0, len(seed), batch):
        output = apply(entries, seed[at:at + batch], capacity)
    responses = checks = 0
    occupancy = []
    statuses = {status: 0 for status in range(1, 5)}
    for turn in range(turns):
        requests = tape[(turn % 100) * batch:(turn % 100 + 1) * batch]
        observed = fold(len(entries), entries.get(requests[0][1], b""))
        checks = (checks + fold(observed, output)) % MOD
        output = apply(entries, requests, capacity)
        responses = fold(responses, output)
        occupancy.append(len(entries))
        at = 0
        while at < len(output):
            status = output[at]
            statuses[status] += 1
            at += 1 + (value_width if status == 4 else 0)
    profile = dict(scope="independent oracle: occupancy after each batch; load argument is eligible key-domain size as percent of capacity",
                   occupancy_min=min(occupancy), occupancy_max=max(occupancy), occupancy_sum=sum(occupancy),
                   occupancy_samples=len(occupancy), capacity=capacity, response_status_counts=statuses)
    return dict(universe=universe, live=len(entries), response_digest=responses, snapshot_checks=checks), {key.hex(): value.hex() for key, value in entries.items()}, counts, profile


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(binary, arguments, expected, entries, prefix):
    command = [str(binary), *arguments, "samples"]
    started = time.monotonic_ns()
    with prefix.with_suffix(".stdout").open("w") as stdout, prefix.with_suffix(".stderr").open("w") as stderr:
        process = subprocess.Popen(command, stdout=stdout, stderr=stderr)
        _, status, usage = os.wait4(process.pid, 0)
        process.returncode = os.waitstatus_to_exitcode(status)
    elapsed = time.monotonic_ns() - started
    if process.returncode:
        raise RuntimeError(f"{command} exited {process.returncode}; see {prefix}.stderr")
    samples, actual_entries, report = [], {}, None
    for line in prefix.with_suffix(".stdout").read_text().splitlines():
        if line.startswith("entry,"):
            _, key, value = line.split(",")
            if key in actual_entries:
                raise RuntimeError("duplicate exported key")
            actual_entries[key] = value
        elif line.startswith("sample,"):
            _, position, duration = line.split(",")
            if int(position) != len(samples) or int(duration) < 0:
                raise RuntimeError("invalid sample")
            samples.append(int(duration))
        elif line.startswith("{"):
            if report is not None:
                raise RuntimeError("duplicate report")
            report = json.loads(line)
        else:
            raise RuntimeError(f"unexpected line: {line}")
    if actual_entries != entries:
        raise RuntimeError(f"{prefix}: final dictionary differs")
    if report is None or len(samples) != int(arguments[5]):
        raise RuntimeError("missing report or samples")
    for key, value in expected.items():
        if report[key] != value:
            raise RuntimeError(f"{prefix}: {key}={report[key]}, expected {value}")
    ordered = sorted(samples)
    for key, p in (("batch_p50_ns", 500), ("batch_p95_ns", 950), ("batch_p99_ns", 990), ("batch_p999_ns", 999), ("batch_max_ns", 1000)):
        if report[key] != ordered[(len(ordered) * p + 999) // 1000 - 1]:
            raise RuntimeError(f"percentile mismatch: {key}")
    for key in ("wall_ns", "allocs", "first_batch_allocs", "fresh_bytes", "startup_ns", "startup_allocs", "startup_fresh_bytes", "seed_allocs"):
        if report[key] < 0:
            raise RuntimeError(f"negative metric: {key}")
    if sum(samples) > report["wall_ns"] or report["first_batch_allocs"] > report["allocs"]:
        raise RuntimeError("inconsistent metrics")
    if arguments[0] == "fip" and arguments[-1] == "unique" and report["allocs"] != 0:
        raise RuntimeError("unique bounded core allocated")
    report.update(command=command, process_wall_ns=elapsed, process_user_s=usage.ru_utime,
                  process_system_s=usage.ru_stime, process_peak_rss_bytes=usage.ru_maxrss * (1 if platform.system() == "Darwin" else 1024),
                  minor_faults=usage.ru_minflt, major_faults=usage.ru_majflt, operation_total_ns=sum(samples))
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compiler", type=Path, required=True)
    parser.add_argument("--capacity", type=int, default=5)
    parser.add_argument("--key-bytes", type=int, default=2)
    parser.add_argument("--value-bytes", type=int, default=9)
    parser.add_argument("--batch", type=int, default=3)
    parser.add_argument("--turns", type=int, default=5)
    parser.add_argument("--loads", type=int, nargs="+", default=[50, 100, 150])
    parser.add_argument("--mixes", nargs="+", choices=["mixed", "read", "write"], default=["mixed", "read", "write"])
    parser.add_argument("--repeats", type=int, default=1)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not (1 <= args.capacity <= 4096 and 1 <= args.key_bytes <= 64 and 8 <= args.value_bytes <= 256 and 1 <= args.batch <= 1024 and 1 <= args.turns <= 1000000 and args.repeats > 0):
        parser.error("invalid dimensions, turns or repeats")
    if any(not 1 <= load <= 200 or args.key_bytes == 1 and max(1, args.capacity * load // 100) > 256 for load in args.loads):
        parser.error("invalid load or key universe")
    target = {("Darwin", "arm64"): "arm64-darwin", ("Linux", "aarch64"): "arm64-linux", ("Linux", "x86_64"): "x86-64-linux"}.get((platform.system(), platform.machine()))
    if target is None:
        parser.error("supported native host required")
    root = Path(__file__).resolve().parent.parent
    compiler = args.compiler.resolve(strict=True)
    args.output.mkdir(parents=True, exist_ok=False)
    output = args.output.resolve()
    source = output / "source"
    source.mkdir()
    inputs = sorted((root / "examples/fip").glob("kv_config*.fern")) + [root / "examples/fip/kv_workload.fern", Path(__file__).resolve(), root / "bootstrap/stage0.lock"]
    inputs += sorted((root / "internal/stdlib").rglob("*.fern"))
    hashes = {}
    for path in inputs:
        destination = source / path.relative_to(root)
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, destination)
        hashes[str(path.relative_to(root))] = digest(path)
    binary = output / "kv"
    command = [str(compiler), "-target", target, "-o", str(binary), str(root / "examples/fip/kv_config.fern"), str(root / "internal/stdlib")]
    subprocess.run(command, check=True, cwd=root, capture_output=True, text=True)
    (output / "metadata.json").write_text(json.dumps(dict(host=platform.platform(), machine=platform.machine(), target=target, compiler=str(compiler), compiler_sha256=digest(compiler), binary_sha256=digest(binary), compile_command=command, sources=hashes, configuration=vars(args) | {"compiler": str(compiler), "output": str(output)}, metric_scope="samples: process only; wall/allocations: process, borrowed snapshot probe, response digest and sample bookkeeping; process CPU/RSS include startup, export and sorting"), indent=2) + "\n")
    oracles = {}
    for mix in args.mixes:
        for load in args.loads:
            expected, entries, counts, profile = reference(args.capacity, args.key_bytes, args.value_bytes, args.batch, args.turns, mix, load)
            oracles[f"{mix}-{load}"] = dict(report=expected, entries=entries, tape_op_counts=counts, workload_profile=profile)
    (output / "oracle.json").write_text(json.dumps(oracles, indent=2) + "\n")
    combinations = [(representation, sharing) for representation in ("fip", "map", "pmap") for sharing in ("unique", "shared")]
    with (output / "results.jsonl").open("w") as results:
        for repeat in range(args.repeats):
            offset = repeat % len(combinations)
            for mix in args.mixes:
                for load in args.loads:
                    oracle = oracles[f"{mix}-{load}"]
                    for representation, sharing in combinations[offset:] + combinations[:offset]:
                        arguments = [representation, str(args.capacity), str(args.key_bytes), str(args.value_bytes), str(args.batch), str(args.turns), mix, str(load), sharing]
                        expected = oracle["report"] | dict(representation=representation, capacity=args.capacity, key_bytes=args.key_bytes, value_bytes=args.value_bytes, batch=args.batch, turns=args.turns, mix=mix, load=load, sharing=sharing)
                        prefix = output / f"{repeat}-{mix}-{load}-{representation}-{sharing}"
                        report = run(binary, arguments, expected, oracle["entries"], prefix)
                        report["repeat"] = repeat
                        results.write(json.dumps(report) + "\n")
                        results.flush()
                        print(f"{prefix.name}: oracle and samples passed", flush=True)


if __name__ == "__main__":
    main()
