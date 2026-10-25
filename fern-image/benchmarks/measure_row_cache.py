"""Collect row cache work and allocator observations in fresh processes."""

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
        raise SystemExit("usage: measure_row_cache.py BINARY COMPILER OUTPUT.json")
    binary, compiler, output = (Path(p).resolve() for p in sys.argv[1:])
    root = Path(__file__).resolve().parents[1]
    sources = ["benchmarks/row_cache.fern", "packages/pipeline/pipeline.fern",
               "packages/resize/resize.fern", "packages/color/color.fern", "packages/pixels/pixels.fern"]
    paths = {"fern-image/" + p: root / p for p in sources}
    paths.update({"compiler": compiler, "binary": binary, "collector": Path(__file__)})
    hashes = {key: digest(path) for key, path in paths.items()}
    version = subprocess.check_output([str(compiler), "-version"], text=True).strip()
    samples = []
    for cache_bytes in [0, 12000]:
        for count in [16, 128]:
            for repeat in range(3):
                result = subprocess.run([str(binary), str(cache_bytes), str(count)],
                                        capture_output=True, text=True, check=True)
                fields = {key: int(value) for key, value in
                          (item.split("=", 1) for item in result.stdout.split())}
                if fields["cache_bytes"] != cache_bytes or fields["count"] != count or fields["checksum"] != count * 17:
                    raise SystemExit("unexpected workload result")
                leak = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", result.stderr)
                if not leak or leak[1] != leak[2]:
                    raise SystemExit("probe did not release every allocation")
                samples.append({"repeat": repeat, **fields, "process_allocations": int(leak[1]), "exit_code": result.returncode})
    if hashes != {key: digest(path) for key, path in paths.items()}:
        raise SystemExit("measurement sources or tools changed")
    record = {
        "workload": "Virtual 1000x10000 RGBA source and bilinear 500x20000 node, consecutive output rows from 10000. Consumer discards rows.",
        "output_bytes_per_row": 2000, "max_logical_scratch_bytes": 28000,
        "max_samples": 320000, "max_cache_entries": 4,
        "concurrency": 1, "warmup_processes": 0,
        "flags": "-target x86-64-linux with FERN_LEAKCHECK=1, no quarantine",
        "compiler_version": version, "sha256": hashes, "host": platform.platform(), "samples": samples,
        "limitations": "Cache peaks count row payload only. Bump bytes count fresh allocator demand, not peak live bytes, peak RSS, or a process memory bound. Counters exclude report construction. Cache metadata and callback allocations are part of the workload. No timing, throughput, OOM-recovery, or production-safety claim."
    }
    output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
