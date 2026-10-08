#!/usr/bin/env -S uv run --script
"""Native same-machine Perceus comparison with independent result oracles.

Compilation is outside measurements. Wall/CPU/RSS cover the complete process,
including startup, output and teardown. Fern census is a separate untimed run.
No timing or instruction count from emulated execution is accepted here.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import resource
import shutil
import signal
import subprocess
import time

NAMES = ("rbtree", "rbtree-ck", "deriv", "nqueens", "cfold")
MAXIMUM = dict(zip(NAMES, (4200000, 4200000, 10, 13, 20)))


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def queens(n):
    mask = (1 << n) - 1

    def visit(columns, left, right):
        if columns == mask:
            return 1
        available = mask & ~(columns | left | right)
        count = 0
        while available:
            bit = available & -available
            available -= bit
            count += visit(columns | bit, (left | bit) << 1, (right | bit) >> 1)
        return count

    return visit(0, 0, 0)


def expected(name, n, root):
    if name == "rbtree":
        return f"{(n + 9) // 10}\n"
    if name == "rbtree-ck":
        return f"{n // 10}\n"
    if name == "nqueens":
        return f"{queens(n)}\n"
    if name == "cfold":
        leaves = {1: 1}
        for _ in range(n):
            after = {}
            for value, count in leaves.items():
                for successor in (value + 1, max(value - 1, 0)):
                    after[successor] = after.get(successor, 0) + count
            leaves = after
        value = sum(value * count for value, count in leaves.items())
        return f"{value}\n{value}\n"
    spec = importlib.util.spec_from_file_location("deriv_oracle", root / "scripts/perceus-deriv-oracle.py")
    oracle = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(oracle)
    x = oracle.node("var", "x")
    expression = oracle.power(x, x)
    lines = []
    for i in range(n):
        expression = oracle.derivative(expression)
        lines.append(f"{i + 1} count: {oracle.count(expression)}")
    return "\n".join(lines + ["done", ""])


def command_run(command, log, cwd=None, env=None):
    with log.open("w") as output:
        completed = subprocess.run(command, cwd=cwd, env=env, stdout=output, stderr=subprocess.STDOUT)
    if completed.returncode:
        raise RuntimeError(f"command failed ({completed.returncode}): {command}; see {log}")


def measure(command, prefix, timeout, env=None, unlimited_stack=False):
    def child_limits():
        if unlimited_stack:
            _, hard = resource.getrlimit(resource.RLIMIT_STACK)
            resource.setrlimit(resource.RLIMIT_STACK, (resource.RLIM_INFINITY, hard))

    with prefix.with_suffix(".stdout").open("wb") as stdout, prefix.with_suffix(".stderr").open("wb") as stderr:
        started = time.perf_counter_ns()
        process = subprocess.Popen(command, stdout=stdout, stderr=stderr, env=env,
                                   preexec_fn=child_limits if unlimited_stack else None)

        def expired(signum, frame):
            raise TimeoutError(f"run exceeded {timeout}s: {command}; raw output at {prefix}")

        previous = signal.signal(signal.SIGALRM, expired)
        signal.setitimer(signal.ITIMER_REAL, timeout)
        try:
            _, status, usage = os.wait4(process.pid, 0)
            elapsed = time.perf_counter_ns() - started
        except TimeoutError:
            signal.setitimer(signal.ITIMER_REAL, 0)
            failure = {"command": command, "pid": process.pid, "timed_out": True,
                       "timeout_seconds": timeout, "exit": None, "reaped": False}
            # Persist the failure before cleanup: a kernel exit wait can remain
            # blocked even after SIGKILL. That is not a measured successful run.
            prefix.with_suffix(".json").write_text(json.dumps(failure, indent=2) + "\n")
            process.kill()
            deadline = time.monotonic() + 2
            while time.monotonic() < deadline:
                pid, status, _ = os.wait4(process.pid, os.WNOHANG)
                if pid:
                    process.returncode = os.waitstatus_to_exitcode(status)
                    failure.update(exit=process.returncode, reaped=True)
                    break
                time.sleep(0.01)
            failure["wall_ns_until_cleanup_finished"] = time.perf_counter_ns() - started
            prefix.with_suffix(".json").write_text(json.dumps(failure, indent=2) + "\n")
            raise
        finally:
            signal.setitimer(signal.ITIMER_REAL, 0)
            signal.signal(signal.SIGALRM, previous)
        process.returncode = os.waitstatus_to_exitcode(status)
    result = {"command": command, "exit": process.returncode,
              "wall_ns": elapsed,
              "user_seconds": usage.ru_utime, "system_seconds": usage.ru_stime,
              "peak_rss_bytes": usage.ru_maxrss * (1 if platform.system() == "Darwin" else 1024),
              "minor_faults": usage.ru_minflt, "major_faults": usage.ru_majflt}
    prefix.with_suffix(".json").write_text(json.dumps(result, indent=2) + "\n")
    if process.returncode:
        raise RuntimeError(f"run exited {process.returncode}: {command}; see {prefix}")
    return result


def main():
    pipeline_started = time.perf_counter_ns()
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fern", type=Path, required=True)
    parser.add_argument("--koka", type=Path, required=True)
    parser.add_argument("--lean", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--reuse-builds", type=Path, help="reuse verified identical compiler/kernel binaries from a prior output")
    parser.add_argument("--case", action="append", required=True, help="workload:size; may repeat")
    parser.add_argument("--repeats", type=int, default=1)
    parser.add_argument("--timeout", type=int, default=60)
    parser.add_argument("--unlimited-stack", action="store_true", help="explicit Linux-only equal stack policy for every workload child")
    parser.add_argument("--environment-label", default="native host", help="identify a VM/container when used")
    args = parser.parse_args()
    machine = platform.machine()
    if machine not in ("arm64", "aarch64", "x86_64") or platform.system() not in ("Darwin", "Linux"):
        parser.error("native Darwin/Linux arm64/x86_64 host required")
    if os.environ.get("QEMU_CPU") or os.environ.get("QEMU_LD_PREFIX"):
        parser.error("emulated performance runs are not supported")
    if args.repeats < 1 or args.timeout < 1:
        parser.error("repeats and timeout must be positive")
    stack_limits = resource.getrlimit(resource.RLIMIT_STACK)
    if args.unlimited_stack and (platform.system() != "Linux" or stack_limits[1] != resource.RLIM_INFINITY):
        parser.error("unlimited-stack requires Linux with an unlimited inherited hard stack limit")
    cases = []
    for case in args.case:
        parts = case.split(":")
        if len(parts) != 2 or parts[0] not in NAMES or not parts[1].isdigit():
            parser.error(f"invalid case {case}")
        name, n = parts[0], int(parts[1])
        if n > MAXIMUM[name]:
            parser.error(f"{name} exceeds the upstream full size {MAXIMUM[name]}")
        cases.append((name, n))
    root = Path(__file__).resolve().parent.parent
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=False)
    sources = out / "sources"
    shutil.copytree(root / "bench/perceus", sources / "perceus")
    shutil.copytree(root / "internal/stdlib", sources / "stdlib")
    for script in ("bench-perceus.py", "perceus-deriv-oracle.py"):
        shutil.copy2(root / "scripts" / script, sources / script)
    target = ("arm64" if machine in ("arm64", "aarch64") else "x86-64") + ("-darwin" if platform.system() == "Darwin" else "-linux")
    versions = {}
    for name, executable in (("koka", args.koka), ("lean", args.lean)):
        versions[name] = subprocess.check_output([str(executable), "--version"], text=True)
    metadata = {"platform": platform.platform(), "machine": machine,
                "environment_label": args.environment_label,
                "logical_cpus": os.cpu_count(),
                "physical_memory_bytes": os.sysconf("SC_PHYS_PAGES") * os.sysconf("SC_PAGE_SIZE"),
                "cgroup_limits": {name: Path("/sys/fs/cgroup", name).read_text().strip() for name in ("memory.max", "cpu.max") if Path("/sys/fs/cgroup", name).exists()},
                "stack_policy": "unlimited" if args.unlimited_stack else "inherited",
                "inherited_stack_limits_bytes": stack_limits,
                "workload_stack_limits_bytes": [resource.RLIM_INFINITY, stack_limits[1]] if args.unlimited_stack else stack_limits,
                "root_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                "target": target, "versions": versions, "cases": cases, "repeats": args.repeats,
                "compilers": {name: {"path": str(path.resolve()), "sha256": sha(path)} for name, path in
                              (("fern", args.fern), ("koka", args.koka), ("lean", args.lean), ("leanc", args.lean.with_name("leanc")))},
                "sources": {str(path.relative_to(sources)): sha(path) for path in sources.rglob("*") if path.is_file()},
                "measurement_scope": "Whole process including startup, printing and teardown; one verified warmup per language/case excluded; separate instrumented untimed Fern allocation census."}
    (out / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    binaries = {}
    builds = []
    previous_builds = None
    if args.reuse_builds:
        prior = args.reuse_builds.resolve()
        previous = json.loads((prior / "metadata.json").read_text())
        if any(previous[key] != metadata[key] for key in ("platform", "machine", "target", "compilers", "versions")):
            raise ValueError("reuse requires identical host, target and compiler binaries/versions")
        relevant = lambda rows: {key: value for key, value in rows.items() if key.endswith((".fern", ".lean", ".kk"))}
        if relevant(previous["sources"]) != relevant(metadata["sources"]):
            raise ValueError("reuse requires identical kernel, driver and standard-library source hashes")
        previous_builds = {(row["language"], row["workload"]): row for row in json.loads((prior / "builds.json").read_text())}
    for name in dict.fromkeys(name for name, _ in cases):
        fern_name = name.replace("-", "_")
        for language in ("fern", "koka", "lean"):
            binary = out / f"{language}-{name}"
            if previous_builds is not None:
                for kind in (("fern", "fern-census") if language == "fern" else (language,)):
                    row = previous_builds[kind, name].copy()
                    source = args.reuse_builds.resolve() / f"{kind}-{name}"
                    if sha(source) != row["binary_sha256"]:
                        raise ValueError(f"previous binary hash mismatch: {source}")
                    destination = out / f"{kind}-{name}"
                    shutil.copy2(source, destination)
                    row["reused_from"] = str(source)
                    builds.append(row)
                    binaries[kind, name] = str(destination)
                continue
            if language == "fern":
                commands = [[str(args.fern.resolve()), "-target", target, "-o", str(binary),
                             str(root / f"bench/perceus/fern/run_{fern_name}.fern"), str(root / "internal/stdlib")]]
            elif language == "koka":
                commands = [[str(args.koka.resolve()), "-O2", "--builddir=" + str(out / "koka-build"), "-o", str(binary),
                             str(root / f"bench/perceus/koka/{name}.kk")]]
            else:
                source = root / f"bench/perceus/lean/{name}.lean"
                cfile = out / f"{name}.c"
                commands = [[str(args.lean.resolve()), "-c", str(cfile), str(source)],
                            [str(args.lean.with_name("leanc").resolve()), "-O3", "-o", str(binary), str(cfile)]]
            for i, command in enumerate(commands):
                command_run(command, out / f"compile-{language}-{name}-{i}.log", cwd=root)
            builds.append({"language": language, "workload": name, "commands": commands, "binary_sha256": sha(binary), "binary_bytes": binary.stat().st_size})
            binaries[language, name] = str(binary)
            if language == "fern":
                census_binary = out / f"fern-census-{name}"
                command = commands[0].copy()
                command[command.index("-o") + 1] = str(census_binary)
                command_run(command, out / f"compile-census-{name}.log", cwd=root, env=dict(os.environ, FERN_LEAKCHECK="1"))
                builds.append({"language": "fern-census", "workload": name, "commands": [command],
                               "environment": {"FERN_LEAKCHECK": "1"}, "binary_sha256": sha(census_binary), "binary_bytes": census_binary.stat().st_size})
                binaries["fern-census", name] = str(census_binary)
    (out / "builds.json").write_text(json.dumps(builds, indent=2) + "\n")
    results = []
    for name, size in cases:
        answer = expected(name, size, root)
        (out / f"expected-{name}-{size}.txt").write_text(answer)
        for language in ("fern", "koka", "lean"):
            prefix = out / f"warm-{name}-{size}-{language}"
            measure([binaries[language, name], str(size)], prefix, args.timeout, unlimited_stack=args.unlimited_stack)
            if prefix.with_suffix(".stdout").read_text() != answer:
                raise AssertionError(f"warmup result mismatch: {language}/{name}/{size}")
        for repeat in range(args.repeats):
            # Rotate order across repeats to avoid always giving one language
            # the same position; preserve actual execution order in results.
            languages = ("fern", "koka", "lean")
            languages = languages[repeat % 3:] + languages[:repeat % 3]
            for language in languages:
                prefix = out / f"run-{name}-{size}-{repeat}-{language}"
                result = measure([binaries[language, name], str(size)], prefix, args.timeout, unlimited_stack=args.unlimited_stack)
                actual = prefix.with_suffix(".stdout").read_text()
                if actual != answer:
                    raise AssertionError(f"{language}/{name}/{size}: {actual!r} != {answer!r}")
                result.update(language=language, workload=name, size=size, repeat=repeat)
                results.append(result)
                (out / "results.json").write_text(json.dumps(results, indent=2) + "\n")
        prefix = out / f"census-{name}-{size}"
        measure([binaries["fern-census", name], str(size)], prefix, args.timeout, unlimited_stack=args.unlimited_stack)
        if prefix.with_suffix(".stdout").read_text() != answer:
            raise AssertionError(f"census changed {name}/{size} result")
        text = prefix.with_suffix(".stderr").read_text()
        match = re.search(r"allocs=(\d+) frees=(\d+) live_bytes=(\d+)", text)
        if not match:
            raise AssertionError(f"missing Fern census: {text}")
        allocs, frees, live = map(int, match.groups())
        if allocs != frees or live:
            raise AssertionError(f"unbalanced Fern census: {text}")
        (out / f"allocations-{name}-{size}.json").write_text(json.dumps({"allocations": allocs, "frees": frees, "live_bytes": live,
            "scope": "whole process; separate from timed runs"}, indent=2) + "\n")
    completion = {"out": str(out), "runs": len(results), "verified": True,
                  "pipeline_elapsed_ns": time.perf_counter_ns() - pipeline_started}
    (out / "completion.json").write_text(json.dumps(completion, indent=2) + "\n")
    print(json.dumps(completion))


if __name__ == "__main__":
    main()
