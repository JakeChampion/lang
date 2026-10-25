"""Collect native self RSS observations and a parent-memory isolation control."""

import hashlib
import json
import platform
import re
import resource
import subprocess
import sys
from pathlib import Path


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def parent_rss():
    for line in Path("/proc/self/status").read_text().splitlines():
        if line.startswith("VmRSS:"):
            return int(line.split()[1]) * 1024
    raise SystemExit("parent VmRSS is unavailable")


def force_fork():
    pass


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: measure_rss.py BINARY COMPILER OUTPUT.json")
    binary, compiler, output = (Path(p).resolve() for p in sys.argv[1:])
    root = Path(__file__).resolve().parents[1]
    names = ["benchmarks/rss_probe.fern", "benchmarks/linux_rss.fern", "packages/pixels/pixels.fern",
             "packages/resize/resize.fern", "packages/color/color.fern", "packages/pipeline/pipeline.fern",
             "packages/pipeline/regions.fern"]
    paths = {"fern-image/" + name: root / name for name in names}
    paths.update({"binary": binary, "compiler": compiler, "collector": Path(__file__)})
    for path in sorted((root.parent / "internal/stdlib").rglob("*")):
        if path.is_file():
            paths["workspace/" + str(path.relative_to(root.parent))] = path
    hashes = {name: digest(path) for name, path in paths.items()}
    modes = ["unique", "shared", "retained", "eager", "eager-retained",
             "rows", "rows-retained", "tiles", "tiles-retained"]
    samples = []
    controls = []
    for padding_bytes in [0, 96 * 1024 * 1024]:
        padding = bytearray(padding_bytes)
        for offset in range(0, padding_bytes, 4096):
            padding[offset] = 1
        selected = modes if padding_bytes == 0 else ["unique", "retained"]
        for mode in selected:
            for count in [8, 32]:
                for repeat in range(3):
                    parent_bytes = parent_rss()
                    # preexec_fn forces fork before exec for this single-threaded collector.
                    result = subprocess.run([str(binary), mode, str(count)], capture_output=True,
                                            text=True, check=True, preexec_fn=force_fork)
                    fields = {key: value if key == "mode" else int(value) for key, value in
                              (field.split("=", 1) for field in result.stdout.split())}
                    if fields["mode"] != mode or fields["count"] != count or fields["checksum"] != count * 17:
                        raise SystemExit("unexpected workload result")
                    leak = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", result.stderr)
                    if not leak or leak[1] != leak[2]:
                        raise SystemExit("probe did not release every allocation")
                    record = {"repeat": repeat, "parent_padding_bytes": padding_bytes,
                              "parent_rss_bytes": parent_bytes,
                              "collector_children_ru_maxrss_bytes": resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss * 1024,
                              **fields, "process_allocations": int(leak[1]), "exit_code": result.returncode}
                    (samples if padding_bytes == 0 else controls).append(record)
        del padding
    if hashes != {name: digest(path) for name, path in paths.items()}:
        raise SystemExit("measurement sources or tools changed")
    record = {
        "compiler_version": subprocess.check_output([str(compiler), "-version"], text=True).strip(),
        "sha256": hashes, "host": platform.platform(), "concurrency": 1, "warmup_processes": 0,
        "flags": "-target x86-64-linux with FERN_LEAKCHECK=1, no quarantine",
        "rss_source": "The Fern worker reads /proc/self/status after exec, while outputs remain live and after workload cleanup.",
        "parent_control": "A single-threaded Python collector forces fork before exec. Control processes inherit 96 MiB of touched parent padding before exec. The children rusage field is cumulative collector data, not isolated worker RSS.",
        "workloads": "Updates use 512x512 RGBA images. Eager nearest resize maps 512x512 to 256x256. Virtual bilinear graphs map 1024x10000 to 512x20000 and return rows or 64x16 cropped tiles. The cache payload limit is 12288 bytes. Generated and eager sources contain solid or transparent pixels. No representative color throughput claim.",
        "limitations": "VmRSS and VmHWM are Linux kernel accounting observations with batching and page-granularity limits. VmHWM is not an exact transient peak or a process memory upper bound. Virtual reservations are not resident bytes. Allocator counters include status observation work. No isolated allocator-overhead, cgroup-charge, OOM-recovery, timeout, or production-safety qualification.",
        "samples": samples, "isolation_controls": controls,
    }
    output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
