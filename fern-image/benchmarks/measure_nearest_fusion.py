"""Compare nearest row implementations with one compiler and fixed workload."""

import hashlib
import json
import platform
import re
import subprocess
import sys
from pathlib import Path


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def source_paths(workspace):
    names = ["benchmarks/nearest_fusion.fern", "packages/pipeline/pipeline.fern", "packages/pixels/pixels.fern",
             "packages/resize/resize.fern", "packages/color/color.fern"]
    paths = {"fern-image/" + name: workspace / "fern-image" / name for name in names}
    for path in sorted((workspace / "internal/stdlib").rglob("*")):
        if path.is_file():
            paths["workspace/" + str(path.relative_to(workspace))] = path
    return paths


def record_baseline(binary, compiler, output):
    workspace = Path.cwd()
    paths = source_paths(workspace)
    paths.update({"compiler": compiler, "binary": binary})
    record = {"source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
              "compiler_version": subprocess.check_output([str(compiler), "-version"], text=True).strip(),
              "sha256": {name: digest(path) for name, path in paths.items()}}
    output.write_text(json.dumps(record, indent=2) + "\n")


def main():
    if len(sys.argv) == 5 and sys.argv[1] == "record-baseline":
        record_baseline(*(Path(p).resolve() for p in sys.argv[2:]))
        return
    if len(sys.argv) != 6:
        raise SystemExit("usage: measure_nearest_fusion.py BASELINE CANDIDATE COMPILER BASELINE_BUILD.json OUTPUT.json")
    baseline, candidate, compiler, provenance, output = (Path(p).resolve() for p in sys.argv[1:])
    original = json.loads(provenance.read_text())
    paths = source_paths(Path(__file__).resolve().parents[2])
    paths.update({"compiler": compiler, "collector": Path(__file__), "baseline_binary": baseline,
                  "candidate_binary": candidate, "baseline_provenance": provenance})
    hashes = {name: digest(path) for name, path in paths.items()}
    pipeline = "fern-image/packages/pipeline/pipeline.fern"
    for name, value in original["sha256"].items():
        expected = hashes["baseline_binary"] if name == "binary" else hashes.get(name)
        if name != pipeline and value != expected:
            raise SystemExit("baseline input or tool changed: " + name)
    if original["sha256"][pipeline] == hashes[pipeline]:
        raise SystemExit("pipeline implementations are identical")
    version = subprocess.check_output([str(compiler), "-version"], text=True).strip()
    if version != original["compiler_version"]:
        raise SystemExit("compiler version changed")
    samples = []
    for count in [8, 32]:
        for repeat in range(3):
            for variant, binary in [("baseline", baseline), ("fused", candidate)]:
                result = subprocess.run([str(binary), str(count)], capture_output=True, text=True, check=True)
                fields = {key: int(value) for key, value in (field.split("=", 1) for field in result.stdout.split())}
                if fields["count"] != count or fields["checksum"] != count * 17:
                    raise SystemExit("unexpected workload output")
                leak = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", result.stderr)
                if not leak or leak[1] != leak[2]:
                    raise SystemExit("probe did not release every allocation")
                samples.append({"variant": variant, "repeat": repeat, **fields,
                                "process_allocations": int(leak[1]), "exit_code": result.returncode})
    if hashes != {name: digest(path) for name, path in paths.items()}:
        raise SystemExit("measurement input or tool changed")
    record = {
        "compiler_version": version, "sha256": hashes,
        "baseline_pipeline_sha256": original["sha256"][pipeline],
        "baseline_source_commit": original["source_commit"], "baseline_build": original,
        "host": platform.platform(), "concurrency": 1, "warmup_processes": 0,
        "flags": "-target x86-64-linux with FERN_LEAKCHECK=1, no quarantine",
        "workload": "RGBA solid source: 1024x8192. Four nearest nodes: 768x12000, 512x16000, 256x20000, 128x24000. The consumer discards each 512-byte output row.",
        "logical_scratch_bytes": 21504, "logical_samples_per_row": 2688,
        "horizontal_coordinate_steps_per_fused_row": 512,
        "horizontal_coordinate_steps_per_baseline_row": 1664,
        "limitations": "Allocator counters cover row requests and output checks after graph construction. Fresh bytes measure allocator demand, not peak live bytes, isolated overhead, RSS, or a process memory bound. No timing, throughput, parity, CPU-deadline, or production-safety claim.",
        "samples": samples,
    }
    output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
