"""Compare allocation events on one immutable compiler workload."""

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
    parser.add_argument("before", type=Path)
    parser.add_argument("after", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--before-source", required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[3]
    subject = args.subject.resolve()
    after_source = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    for commit in [args.before_source, after_source]:
        if not re.fullmatch(r"[0-9a-f]{40}", commit):
            raise ValueError("expected a complete source commit")
        subprocess.run(["git", "cat-file", "-e", commit + "^{commit}"], cwd=root, check=True)
    binaries = {label: getattr(args, label).resolve() for label in ["before", "after"]}
    paths = {**binaries, "builder": args.builder.resolve(), "collector": Path(__file__).resolve(),
             "python": Path(sys.executable).resolve()}
    for prefix, tree in [("subject", subject), ("candidate", root)]:
        for directory in [tree / "compiler", tree / "internal/stdlib"]:
            for path in sorted(directory.rglob("*")):
                if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                    paths[prefix + "/" + str(path.relative_to(tree))] = path
    hashes = {name: sha(path) for name, path in paths.items()}
    samples = []
    with tempfile.TemporaryDirectory(prefix="fern-function-facts-") as temporary:
        output = Path(temporary) / "checker.s"
        for repeat in range(3):
            for label in (["before", "after"] if repeat % 2 == 0 else ["after", "before"]):
                command = [str(binaries[label]), "-target", "x86-64-linux", "-emit", "asm", "-o", str(output),
                           str(subject / "compiler/checker.fern"), str(subject / "internal/stdlib")]
                result = subprocess.run(command, cwd=subject, env={**os.environ, "FERN_RC_CERTIFY": "0"},
                                        text=True, capture_output=True, timeout=120)
                census = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", result.stderr)
                if (result.returncode or result.stdout or not census or census[1] != census[2]
                        or int(census[1]) == 0 or not output.stat().st_size):
                    raise ValueError(f"{label}: {result.returncode} {result.stdout[-500:]} {result.stderr[-1000:]}")
                samples.append({"compiler": label, "repeat": repeat, "allocations": int(census[1]),
                                "frees": int(census[2]), "live_bytes": 0, "exit_code": 0,
                                "assembly_sha256": sha(output), "assembly_bytes": output.stat().st_size})
                print(label, repeat, census[0].strip(), flush=True)
    if hashes != {name: sha(path) for name, path in paths.items()}:
        raise ValueError("sources or tools changed")
    for label in binaries:
        selected = [s for s in samples if s["compiler"] == label]
        if len({(s["allocations"], s["assembly_sha256"]) for s in selected}) != 1:
            raise ValueError("unstable repeated observation: " + label)
    if len({s["assembly_sha256"] for s in samples}) != 1:
        raise ValueError("candidate changed the emitted assembly")
    record = {"subject_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=subject, text=True).strip(),
              "compiler_source_commits": {"before": args.before_source, "after": after_source},
              "builder_version": subprocess.check_output([str(paths["builder"]), "-version"], text=True).strip(),
              "instrumented_build": ["FERN_LEAKCHECK=1", "builder", "-target", "x86-64-linux", "-o", "binary", "compiler/fern.fern"],
              "run_flags": ["FERN_RC_CERTIFY=0", "-target", "x86-64-linux", "-emit", "asm"],
              "sha256": hashes, "samples": samples,
              "limits": {"process_deadline_seconds": 120, "repeats": 3, "concurrency": 1},
              "limitations": "Allocation events on one fixed input. Both compilers use the same builder and runtime libraries. No CPU timing, peak live bytes, allocator overhead, RSS, or production qualification."}
    args.output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
