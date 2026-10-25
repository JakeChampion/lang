"""Collect process observations for the synthetic row benchmark."""

import hashlib
import json
import os
import platform
import subprocess
import sys
import time
from pathlib import Path


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: measure_rows.py BINARY COMPILER OUTPUT.json")
    binary, compiler, output = (Path(p).resolve() for p in sys.argv[1:])
    root = Path(__file__).resolve().parents[1]
    sources = ["benchmarks/row_pipeline.fern", "packages/pipeline/pipeline.fern",
               "packages/resize/resize.fern", "packages/pixels/pixels.fern"]
    fingerprints = {"fern-image/" + p: digest(root / p) for p in sources}
    samples = []
    for _ in range(3):
        start = time.perf_counter()
        pid = os.fork()
        if pid == 0:
            os.execv(str(binary), [str(binary)])
        _, status, usage = os.wait4(pid, 0)
        code = os.waitstatus_to_exitcode(status)
        if code:
            raise SystemExit(f"row benchmark failed: {code}")
        samples.append({"wall_ms": (time.perf_counter() - start) * 1000,
                        "peak_rss_kib": usage.ru_maxrss,
                        "user_seconds": usage.ru_utime,
                        "system_seconds": usage.ru_stime, "exit_code": code})
    if fingerprints != {"fern-image/" + p: digest(root / p) for p in sources}:
        raise SystemExit("benchmark sources changed during measurement")
    record = {
        "workload": "Generated 1000x10000 RGBA source, nearest node 500x20000, five sparse output rows. No full source or destination pixel array.",
        "output_rows": [0, 5000, 10000, 15000, 19999],
        "logical_source_pixels": 10000000, "logical_output_pixels": 10000000,
        "output_bytes_per_row": 2000, "logical_scratch_limit_bytes": 12000,
        "sample_limit": 1500, "concurrency": 1, "warmup_processes": 0,
        "flags": "-target x86-64-linux, no sanitizer", "host": platform.platform(),
        "cpu": next((line.split(":", 1)[1].strip()
                     for line in Path("/proc/cpuinfo").read_text().splitlines()
                     if line.startswith("model name")), "unknown"),
        "compiler": subprocess.check_output([str(compiler), "-version"], text=True).strip(),
        "compiler_sha256": digest(compiler), "binary_sha256": digest(binary),
        "sources_sha256": fingerprints, "samples": samples,
        "limitations": "Linux wait4 child RSS includes the Python collector inherited at fork before exec. This is not isolated Fern RSS. Wall time includes fork, startup, validation, rendering, and cleanup. Shared host, no isolation. No claim of allocator recovery, memory upper bound, real-image throughput, or libvips parity."
    }
    output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
