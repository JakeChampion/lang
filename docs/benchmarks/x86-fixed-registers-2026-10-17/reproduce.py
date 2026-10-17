#!/usr/bin/env python3
"""Run on an idle host: reproduce.py BASELINE CANDIDATE ROOT OUTPUT."""
import hashlib
import json
from pathlib import Path
import platform
import re
import shutil
import statistics
import subprocess
import sys
import time

baseline, candidate, root, out = map(lambda x: Path(x).resolve(), sys.argv[1:])
out.mkdir(parents=True, exist_ok=True)
input_file = out / "input"
input_file.write_bytes(bytes(range(256)) * 32768)
digest = hashlib.sha256(input_file.read_bytes()).hexdigest()
software = out / "software-stdlib"
shutil.copytree(root / "internal/stdlib", software, dirs_exist_ok=True)
crypto = software / "std/crypto.fern"
text = crypto.read_text()
assert text.count("if (!__sha256_hw()) {") == 1
crypto.write_text(text.replace("if (!__sha256_hw()) {", "if (true) {"))
compilers = {"baseline": baseline, "candidate": candidate}
result = {
    "host": platform.platform(),
    "valgrind": subprocess.check_output(["valgrind", "--version"], text=True).strip(),
    "compiler_sha256": {
        name: hashlib.sha256(path.read_bytes()).hexdigest()
        for name, path in compilers.items()
    },
    "input_sha256": digest,
    "input_bytes": input_file.stat().st_size,
    "sha256": {},
}


def pairs(programs, args, expected, repetitions=21):
    rows = []
    for i in range(repetitions):
        row = {}
        order = ["baseline", "candidate"] if i % 2 == 0 else ["candidate", "baseline"]
        for name in order:
            start = time.perf_counter()
            process = subprocess.run([str(programs[name]), *args], capture_output=True, check=True)
            row[name] = time.perf_counter() - start
            assert process.stdout == expected
        row["ratio"] = row["candidate"] / row["baseline"]
        rows.append(row)
    return {"pairs": rows, "median_ratio": statistics.median(row["ratio"] for row in rows)}


for mode, stdlib in [("hardware", root / "internal/stdlib"), ("software", software)]:
    measurement = {"builds": {}, "instructions": {}}
    programs = {}
    for name, compiler in compilers.items():
        binary = out / (name + "-" + mode)
        programs[name] = binary
        asm = Path(str(binary) + ".s")
        source = root / "coreutils/sha256sum.fern"
        for extra, output in [([], binary), (["-emit", "asm"], asm)]:
            subprocess.run([
                str(compiler), "-O", "-target", "x86-64-linux", *extra,
                "-o", str(output), str(source), str(stdlib),
            ], check=True)
        text = asm.read_text()
        marker = "__fn_crypto____sha256_soft_blocks:\n"
        body = text.split(marker, 1)[1].split(".cfi_endproc", 1)[0] if marker in text else ""
        measurement["builds"][name] = {
            "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
            "binary_bytes": binary.stat().st_size,
            "software_function_instructions": len(re.findall(r"(?m)^    [a-z]", body)),
            "software_function_frame_references": body.count("(%rbp)"),
        }
        counts = out / (name + "-" + mode + ".cachegrind")
        process = subprocess.run([
            "valgrind", "--tool=cachegrind", "--cache-sim=no", "--branch-sim=no",
            "--cachegrind-out-file=" + str(counts), str(binary), str(input_file),
        ], capture_output=True, check=True)
        assert process.stdout.split()[0].decode() == digest
        Path(str(counts) + ".log").write_bytes(process.stderr)
        measurement["instructions"][name] = int(re.search(r"(?m)^summary: (\d+)", counts.read_text())[1])
    expected = (digest + "  " + str(input_file) + "\n").encode() * 16
    measurement["native_files_per_run"] = 16
    measurement["native_bytes_per_run"] = input_file.stat().st_size * 16
    measurement["native"] = pairs(programs, [str(input_file)] * 16, expected)
    result["sha256"][mode] = measurement
    (out / "results.json").write_text(json.dumps(result, indent=2) + "\n")

programs = {}
for name, compiler in compilers.items():
    binary = out / (name + "-nine-values")
    programs[name] = binary
    subprocess.run([
        str(compiler), "-O", "-target", "x86-64-linux", "-o", str(binary),
        str(Path(__file__).with_name("nine-live-values.fern")), str(root / "internal/stdlib"),
    ], check=True)
expected = subprocess.check_output([str(programs["baseline"])])
result["nine_values"] = pairs(programs, [], expected, repetitions=15)
result["nine_values"]["checksum"] = expected.decode().strip()
(out / "results.json").write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps({mode: row["native"]["median_ratio"] for mode, row in result["sha256"].items()}))
print("nine_values", result["nine_values"]["median_ratio"])
