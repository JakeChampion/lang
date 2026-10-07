#!/usr/bin/env -S uv run --script
"""Compare inline and generic-ring brokers using the unchanged broker oracle."""
import argparse
import importlib.util
import json
from pathlib import Path
import platform
import shutil
import subprocess


def adapt(source):
    replacements = {
        'import "./broker_core";': 'import "./broker_core";\nimport "./bounded_ring";',
        "b.queues[b.heads[0]]": "bounded_ring.front_or(b.queues[0], -1)",
        "b.lengths[0]": "b.queues[0].count",
        "b.queues[(b.heads[0] + i) % b.queue_capacity]": "bounded_ring.at_or(b.queues[0], i, -1)",
    }
    for old, new in replacements.items():
        if source.count(old) != 1:
            raise RuntimeError(f"driver adapter expected exactly one occurrence: {old}")
        source = source.replace(old, new)
    return source


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
        parser.error("full mode needs an extra producer slot, so occupancy must be <=4095")
    target = {("Darwin", "arm64"): "arm64-darwin", ("Linux", "aarch64"): "arm64-linux", ("Linux", "x86_64"): "x86-64-linux"}.get((platform.system(), platform.machine()))
    if target is None:
        parser.error("a supported native host is required")
    root = Path(__file__).resolve().parent.parent
    spec = importlib.util.spec_from_file_location("broker_oracle", root / "scripts/bench-broker.py")
    oracle = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(oracle)
    compiler = options.compiler.resolve(strict=True)
    options.output.mkdir(parents=True, exist_ok=False)
    output = options.output.resolve()
    sources = output / "source"
    sources.mkdir()
    inputs = [root / "examples/fip" / name for name in ("broker.fern", "broker_core.fern", "broker_ring.fern", "bounded_ring.fern")]
    inputs += [Path(__file__).resolve(), root / "scripts/bench-broker.py", root / "bootstrap/stage0.lock", root / "internal/stdlib/std/bench.fern"]
    hashes = {}
    for path in inputs:
        shutil.copy2(path, sources / path.name)
        hashes[str(path.relative_to(root))] = oracle.digest(path)
    binaries = {}
    builds = {}
    for representation, core in (("inline", "broker_core.fern"), ("ring", "broker_ring.fern")):
        directory = sources / representation
        directory.mkdir()
        text = (sources / "broker.fern").read_text()
        (directory / "broker.fern").write_text(adapt(text) if representation == "ring" else text)
        shutil.copy2(sources / core, directory / "broker_core.fern")
        shutil.copy2(sources / "bounded_ring.fern", directory / "bounded_ring.fern")
        binary = output / representation
        command = [str(compiler), "-target", target, "-o", str(binary), str(directory / "broker.fern"), str(root / "internal/stdlib")]
        with (output / f"compile-{representation}.log").open("w") as log:
            subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
        binaries[representation] = binary
        builds[representation] = dict(command=command, binary_sha256=oracle.digest(binary),
                                      generated_sources={p.name: oracle.digest(p) for p in directory.glob("*.fern")})
    metadata = dict(host=platform.platform(), compiler=str(compiler), compiler_sha256=oracle.digest(compiler),
                    revision=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                    working_tree=subprocess.check_output(["git", "status", "--short"], cwd=root, text=True),
                    sources=hashes, builds=builds, subscribers=options.subscribers, occupancies=options.occupancies,
                    turns=options.turns, repeats=options.repeats, modes=options.modes,
                    measurement="Only queue representation and logical access differ. Identical driver and independent broker oracle. Startup includes pool, queues, sample storage and initial payload creation; fill allocations are also separate. Shared retains an external Message, not the whole Broker. Samples time turnover or refusal; wall also includes alias checks and sample storage. Export and sorting follow marks. Process CPU/RSS/faults include startup and export. Fresh bytes are high-water growth, not requested bytes; faults are not cache misses.")
    (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    expected = {f"{s}-{n}-{mode}": oracle.reference(s, n, options.turns, mode)
                for s in options.subscribers for n in options.occupancies for mode in options.modes}
    (output / "oracle.json").write_text(json.dumps(expected, indent=2) + "\n")
    configurations = [(mode, sharing, representation) for mode in options.modes
                      for sharing in ("unique", "shared") for representation in binaries]
    with (output / "results.jsonl").open("w") as results:
        for repeat in range(options.repeats):
            offset = repeat % len(configurations)
            for subscribers in options.subscribers:
                for occupancy in options.occupancies:
                    for mode, sharing, representation in configurations[offset:] + configurations[:offset]:
                        prefix = output / f"{repeat}-{subscribers}-{occupancy}-{mode}-{sharing}-{representation}"
                        report = oracle.run(binaries[representation], subscribers, occupancy, options.turns, mode, sharing, prefix)
                        report.update(repeat=repeat, representation=representation)
                        results.write(json.dumps(report) + "\n")
                        results.flush()
                        print(f"{prefix.name}: oracle and samples passed", flush=True)


if __name__ == "__main__":
    main()
