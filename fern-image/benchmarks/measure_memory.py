"""Collect native allocator observations in fresh processes."""

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
        raise SystemExit("usage: measure_memory.py BINARY COMPILER OUTPUT.json")
    binary, compiler, output = (Path(p).resolve() for p in sys.argv[1:])
    root = Path(__file__).resolve().parents[1]
    sources = ["benchmarks/memory_probe.fern", "packages/pipeline/pipeline.fern",
               "packages/resize/resize.fern", "packages/color/color.fern",
               "packages/pixels/pixels.fern"]
    fingerprints = {"fern-image/" + p: digest(root / p) for p in sources}
    samples = []
    for mode in ["unique", "shared", "retained", "rows", "rows-retained", "rows-cancelled"]:
        for count in [16, 128]:
            for repeat in range(3):
                result = subprocess.run([str(binary), mode, str(count)],
                                        capture_output=True, text=True, check=True)
                fields = dict(item.split("=", 1) for item in result.stdout.split())
                if fields.pop("mode") != mode:
                    raise SystemExit("unexpected mode")
                fields = {key: int(value) for key, value in fields.items()}
                if fields["count"] != count or fields["checksum"] != count * 17:
                    raise SystemExit("unexpected workload result")
                leak = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?",
                                    result.stderr)
                if not leak or leak[1] != leak[2]:
                    raise SystemExit("probe did not release every allocation")
                samples.append({"mode": mode, "repeat": repeat, **fields,
                                "process_allocations": int(leak[1]), "exit_code": result.returncode})
    if fingerprints != {"fern-image/" + p: digest(root / p) for p in sources}:
        raise SystemExit("benchmark sources changed during measurement")
    record = {
        "workload": "64x64 RGBA pixel updates, or rows from a virtual 64x10000 RGBA source and a bilinear 32x20000 node.",
        "image_bytes": 16384, "output_row_bytes": 128,
        "concurrency": 1, "warmup_processes": 0,
        "flags": "-target x86-64-linux with FERN_LEAKCHECK=1, no quarantine",
        "compiler": subprocess.check_output([str(compiler), "-version"], text=True).strip(),
        "compiler_sha256": digest(compiler), "binary_sha256": digest(binary),
        "sources_sha256": fingerprints, "host": platform.platform(), "samples": samples,
        "limitations": "Fresh-process allocator observations only. Bump bytes measure fresh allocator demand, not live bytes, peak RSS, or a process memory bound. Counters exclude report construction. They include input, graph, temporary, and retained-output allocations during each workload. No timing, throughput, OOM-recovery, or production-safety claim."
    }
    output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
