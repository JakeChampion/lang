#!/usr/bin/env python3
"""measure-reads.py BASELINE ARITHMETIC_ONLY READS ROOT OUTPUT (idle Linux host)."""
import hashlib
import json
import os
from pathlib import Path
import re
import statistics
import subprocess
import sys
import time

baseline, arithmetic, reads, root, out = map(lambda p: Path(p).resolve(), sys.argv[1:])
os.sched_setaffinity(0, {int(os.environ.get("FERN_BENCH_CPU", "0"))})
out.mkdir(parents=True, exist_ok=True)
source = Path(__file__).with_name("nine-live-record.fern")
compilers = {"baseline": baseline, "arithmetic": arithmetic, "reads": reads}
report = {"source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(), "cpu": sorted(os.sched_getaffinity(0)), "builds": {}, "pairs": []}
programs = {}
expected = None
for name, compiler in compilers.items():
    binary = out / name
    programs[name] = binary
    for extra, output in [([], binary), (["-emit", "asm"], out / (name + ".s"))]:
        subprocess.run([str(compiler), "-target", "x86-64-linux", *extra, "-o", str(output), str(source), str(root / "internal/stdlib")], check=True)
    text = (out / (name + ".s")).read_text().split("__fn_combine:\n", 1)[1].split(".cfi_endproc", 1)[0]
    counts = out / (name + ".cachegrind")
    process = subprocess.run(["valgrind", "--tool=cachegrind", "--cache-sim=no", "--branch-sim=no", "--cachegrind-out-file=" + str(counts), str(binary)], capture_output=True, check=True)
    if expected is None:
        expected = process.stdout
    assert process.stdout == expected
    (out / (name + ".cachegrind.log")).write_bytes(process.stderr)
    report["builds"][name] = {
        "compiler_sha256": hashlib.sha256(compiler.read_bytes()).hexdigest(),
        "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "instructions": int(re.search(r"(?m)^summary: (\d+)", counts.read_text())[1]),
        "saved_register_pushes": len(re.findall(r"(?m)^    pushq %(?:rbx|r12|r13|r14|r15)$", text)),
        "frame_references": text.count("(%rbp)"),
    }
for i in range(15):
    row = {}
    order = list(compilers) if i % 2 == 0 else list(reversed(compilers))
    for name in order:
        start = time.perf_counter()
        process = subprocess.run([str(programs[name])], capture_output=True, check=True)
        row[name] = time.perf_counter() - start
        assert process.stdout == expected
    row["reads_vs_baseline"] = row["reads"] / row["baseline"]
    row["reads_vs_arithmetic"] = row["reads"] / row["arithmetic"]
    report["pairs"].append(row)
report["checksum"] = expected.decode().strip()
report["median_reads_vs_baseline"] = statistics.median(row["reads_vs_baseline"] for row in report["pairs"])
report["median_reads_vs_arithmetic"] = statistics.median(row["reads_vs_arithmetic"] for row in report["pairs"])
(out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
print(json.dumps({k: v for k, v in report.items() if k != "pairs"}, indent=2))
