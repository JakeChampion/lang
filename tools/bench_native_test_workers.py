"""Compare isolated test workers on one native runner without changing coverage."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import resource
import subprocess
import time


def collect_outcomes(directory):
    rows = json.loads((directory / "summary.json").read_text())
    outcomes = {}
    parents = set()
    for row in rows:
        if row.get("error"):
            raise RuntimeError(row["error"])
        for name in row["tests"]:
            if name in parents:
                raise RuntimeError(f"duplicate parent: {name}")
            parents.add(name)
        for name, outcome in row["outcomes"].items():
            if name in outcomes or outcome not in ("pass", "skip"):
                raise RuntimeError(f"duplicate or unsuccessful outcome: {name}")
            if name.split("/", 1)[0] not in row["tests"]:
                raise RuntimeError(f"outcome belongs to an unassigned parent: {name}")
            outcomes[name] = outcome
    if not parents or not parents.issubset(outcomes):
        raise RuntimeError("missing parent outcomes")
    return parents, outcomes


def verify_events(path, parents):
    started, outcomes = set(), {}
    package_passed = False
    for line in path.read_text().splitlines():
        event = json.loads(line)
        name, action = event.get("Test"), event["Action"]
        if not name:
            if action in ("fail", "skip"):
                raise RuntimeError("baseline package did not pass")
            if action == "pass":
                if package_passed:
                    raise RuntimeError("duplicate baseline package outcome")
                package_passed = True
            continue
        if name.split("/", 1)[0] not in parents:
            raise RuntimeError(f"unexpected baseline test: {name}")
        if action == "run":
            if name in started:
                raise RuntimeError(f"duplicate baseline start: {name}")
            started.add(name)
        elif action in ("pass", "skip", "fail"):
            if name not in started or name in outcomes or action == "fail":
                raise RuntimeError(f"invalid baseline outcome: {name}")
            outcomes[name] = action
    if not package_passed or started != outcomes.keys() or not parents.issubset(outcomes):
        raise RuntimeError("incomplete baseline outcomes")
    return parents, outcomes


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--runner", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--scale", choices=("pilot", "full"), required=True)
    parser.add_argument("--weights", type=Path, required=True)
    args = parser.parse_args()
    if platform.system() != "Linux" or platform.machine() != "x86_64":
        raise RuntimeError("requires native Linux x86-64; emulation is not timing evidence")
    cpus = len(os.sched_getaffinity(0))
    if cpus < 2:
        raise RuntimeError("the comparison needs at least two CPUs")
    args.output.mkdir(parents=True, exist_ok=False)
    pattern = ("^TestX86_64(ExitCode|Arithmetic|ControlFlow|StringLiteralLen)$"
               if args.scale == "pilot" else "^TestX86_64")
    metadata = {
        "revision": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
        "binary_sha256": hashlib.sha256(args.binary.read_bytes()).hexdigest(),
        "runner_sha256": hashlib.sha256(args.runner.read_bytes()).hexdigest(),
        "weights_sha256": hashlib.sha256(args.weights.read_bytes()).hexdigest(),
        "platform": platform.platform(), "cpus": cpus, "scale": args.scale,
        "pattern": pattern,
    }
    (args.output / "environment.json").write_text(json.dumps(metadata, indent=2) + "\n")
    observations = []
    expected = None
    root = Path(__file__).resolve().parents[1]
    names = subprocess.check_output([str(args.binary.resolve()), "-test.list", pattern],
                                    cwd=root / "internal/e2e", text=True).splitlines()
    parents = set(names)
    if not parents or len(parents) != len(names) or any(not name.startswith("Test") for name in names):
        raise RuntimeError("invalid baseline inventory")
    (args.output / "inventory.txt").write_text("\n".join(names) + "\n")
    env = dict(os.environ, GOMAXPROCS=str(cpus))
    started = time.perf_counter()
    for index, weighted in enumerate((False, True, True, False)):
        workers = 2
        directory = args.output / f"trial-{index}-weighted-{int(weighted)}"
        print(f"Starting trial {index}: weighted={weighted}, two workers, total CPU budget {cpus}", flush=True)
        before = resource.getrusage(resource.RUSAGE_CHILDREN)
        begin = time.perf_counter()
        command = [str(args.runner.resolve()), "-binary", str(args.binary.resolve()),
                   "-output", str(directory.resolve()), "-workers", str(workers),
                   "-cpus", str(cpus), "-timeout", "25m", "-run", pattern]
        if weighted:
            command += ["-weights", str(args.weights.resolve())]
        subprocess.run(command, cwd=root / "internal/e2e", env=env, check=True)
        wall = time.perf_counter() - begin
        after = resource.getrusage(resource.RUSAGE_CHILDREN)
        actual_parents, outcomes = collect_outcomes(directory)
        if actual_parents != parents:
            raise RuntimeError("workers omitted or added a selected parent")
        for worker in json.loads((directory / "summary.json").read_text()):
            _, events = verify_events(directory / f"worker-{worker['worker']}.jsonl", set(worker["tests"]))
            if events != worker["outcomes"]:
                raise RuntimeError("summary does not match raw events")
        if expected is not None and (actual_parents, outcomes) != expected:
            raise RuntimeError("test inventory or outcomes changed between worker counts")
        expected = actual_parents, outcomes
        row = {
            "workers": workers, "weighted": weighted, "wall_seconds": wall,
            "cpu_seconds": after.ru_utime + after.ru_stime - before.ru_utime - before.ru_stime,
            "parents": len(actual_parents), "outcomes": len(outcomes),
            "skips": sum(value == "skip" for value in outcomes.values()),
        }
        observations.append(row)
        (args.output / "summary.json").write_text(json.dumps(observations, indent=2) + "\n")
        print(json.dumps(row), flush=True)
    if args.scale == "pilot" and time.perf_counter() - started >= 60:
        raise RuntimeError("pilot exceeded one minute; do not scale up")
    print("All four runs have identical parent inventories and outcomes.", flush=True)


if __name__ == "__main__":
    main()
