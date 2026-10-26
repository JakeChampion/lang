"""Compare allocation counters on one fixed compiler source tree."""

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("subject", type=Path)
    parser.add_argument("builder", type=Path)
    parser.add_argument("baseline", type=Path)
    parser.add_argument("before", type=Path)
    parser.add_argument("after", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--compiler-source-commits", nargs=3, required=True,
                        metavar=("BASELINE", "BEFORE", "AFTER"))
    args = parser.parse_args()
    subject = args.subject.resolve()
    binaries = {label: getattr(args, label).resolve() for label in ["baseline", "before", "after"]}
    source_commits = dict(zip(binaries, args.compiler_source_commits))
    for commit in source_commits.values():
        if not re.fullmatch(r"[0-9a-f]{40}", commit):
            raise ValueError("expected a complete source commit")
        subprocess.run(["git", "cat-file", "-e", commit + "^{commit}"], cwd=subject, check=True)
    paths = {**binaries, "builder": args.builder.resolve(), "collector": Path(__file__).resolve(),
             "python": Path(sys.executable).resolve()}
    for directory in [subject / "compiler", subject / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(subject))] = path
    hashes = {name: sha(path) for name, path in paths.items()}
    environment = {**os.environ, "FERN_RC_CERTIFY": "0"}
    rows = []
    with tempfile.TemporaryDirectory(prefix="fern-view-alloc-") as temporary:
        output = Path(temporary) / "checker.s"
        for repeat in range(3):
            order = ["baseline", "before", "after"] if repeat % 2 == 0 else ["after", "before", "baseline"]
            for label in order:
                command = [str(binaries[label]), "-target", "x86-64-linux", "-emit", "asm", "-o", str(output),
                           str(subject / "compiler/checker.fern"), str(subject / "internal/stdlib")]
                result = subprocess.run(command, cwd=subject, env=environment, text=True, capture_output=True, timeout=120)
                census = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", result.stderr)
                if result.returncode or result.stdout or not census or census[1] != census[2] or int(census[1]) == 0:
                    raise ValueError(f"{label} exit {result.returncode}: {result.stdout[-500:]} {result.stderr[-1000:]}")
                if output.stat().st_size == 0:
                    raise ValueError("empty assembly output")
                rows.append({"compiler": label, "repeat": repeat, "allocations": int(census[1]),
                             "frees": int(census[2]), "live_bytes": 0, "exit_code": result.returncode,
                             "assembly_sha256": sha(output), "assembly_bytes": output.stat().st_size})
                print(label, repeat, census[0].strip(), flush=True)
    if hashes != {name: sha(path) for name, path in paths.items()}:
        raise ValueError("fixed sources or tools changed")
    for label in binaries:
        selected = [row for row in rows if row["compiler"] == label]
        if len({(row["allocations"], row["assembly_sha256"]) for row in selected}) != 1:
            raise ValueError("unstable repeated observation: " + label)
    record = {"subject_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=subject, text=True).strip(),
              "builder_version": subprocess.check_output([str(args.builder.resolve()), "-version"], text=True).strip(),
              "declared_compiler_source_commits": source_commits,
              "instrumented_build": ["FERN_LEAKCHECK=1", "builder", "-target", "x86-64-linux", "-o", "binary", "compiler/fern.fern"],
              "run_flags": ["FERN_RC_CERTIFY=0", "-target", "x86-64-linux", "-emit", "asm"],
              "limits": {"process_deadline_seconds": 120, "repeats": 3, "concurrency": 1},
              "sha256": hashes, "samples": rows,
              "limitations": "Heap event counts on one fixed input. All compiler snapshots use the same emitter and embedded runtime libraries. No CPU timing, peak live bytes, allocator overhead, RSS, compatibility, or production claim."}
    args.output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
