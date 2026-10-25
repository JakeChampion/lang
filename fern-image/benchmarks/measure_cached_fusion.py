"""Record fresh-process cached fusion work and allocator counters."""

import hashlib
import json
import platform
import re
import subprocess
import sys
from pathlib import Path


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: measure_cached_fusion.py BINARY COMPILER OUTPUT.json")
    binary, compiler, output = (Path(p).resolve() for p in sys.argv[1:])
    root = Path(__file__).resolve().parents[1]
    paths = {"compiler": compiler, "binary": binary, "collector": Path(__file__),
             "python": Path(sys.executable).resolve(),
             "fern-image/benchmarks/cached_fusion.fern": root / "benchmarks/cached_fusion.fern"}
    for directory in [root / "packages", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    hashes = {name: digest(path) for name, path in paths.items()}
    samples = []
    for cache_bytes in [0, 12000]:
        for count in [8, 32]:
            for repeat in range(3):
                modes = ["legacy", "fused"] if repeat % 2 == 0 else ["fused", "legacy"]
                for mode in modes:
                    result = subprocess.run([str(binary), mode, str(cache_bytes), str(count)],
                                            capture_output=True, text=True, check=True, timeout=20)
                    fields = dict(item.split("=", 1) for item in result.stdout.split())
                    if fields.pop("mode") != mode:
                        raise ValueError("unexpected mode")
                    fields = {name: int(value) for name, value in fields.items()}
                    if fields["cache_bytes"] != cache_bytes or fields["count"] != count or fields["checksum"] != count * 17:
                        raise ValueError("unexpected workload result")
                    if fields["peak_cache_bytes"] > cache_bytes or fields["peak_cache_entries"] > 10:
                        raise ValueError("cache payload or entry limit exceeded")
                    if cache_bytes == 0 and (fields["hits"] != 0 or fields["misses"] != count * (5 if mode == "legacy" else 2)):
                        raise ValueError("unexpected uncached row work")
                    leak = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", result.stderr)
                    if not leak or leak[1] != leak[2]:
                        raise ValueError("probe allocations remain live")
                    samples.append({"mode": mode, "repeat": repeat, **fields,
                                    "process_allocations": int(leak[1]), "exit_code": result.returncode})
    if hashes != {name: digest(path) for name, path in paths.items()}:
        raise ValueError("sources or tools changed")
    record = {
        "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
        "compiler_version": subprocess.check_output([str(compiler), "-version"], text=True).strip(),
        "sha256": hashes, "host": platform.platform(), "host_machine": platform.machine(),
        "workload": "Virtual solid RGBA 1024x8192, then nearest nodes 768x10000, 512x16000, 256x20000, 128x24000. Consecutive output rows start at 12000. The consumer checks endpoint bytes and discards rows.",
        "control": "One binary selects exactly one legacy or fused fold per process. Both modes use identical graph, consumer, compiler, and stdlib sources.",
        "flags": ["-target", "x86-64-linux", "FERN_LEAKCHECK=1"], "quarantine": False,
        "logical_scratch_estimate_bytes": 21504, "logical_samples_per_row": 2688,
        "limits": {"max_pixels": 10000000, "max_row_bytes": 4096, "max_scratch_bytes": 40000,
                   "max_samples": 100000, "max_nodes": 5, "max_cache_entries": 10,
                   "process_deadline_seconds": 20, "concurrency": 1},
        "warmup_processes": 0, "repeats": 3, "samples": samples,
        "limitations": "Work and allocator counters, without timing or RSS measurements. Cache peaks count row payload only. Bump deltas count fresh allocator demand, not peak live bytes, allocator overhead, or a process memory bound. Counters include graph, cache metadata, and callback work but exclude report construction. Solid input does not qualify representative color throughput. No performance parity, hard deadline, aggregate service, or production-safety claim.",
    }
    output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
