"""Check function-local and aggregate-view diagnostics on three execution targets."""

import argparse
import hashlib
import json
import os
import re
import subprocess
import tempfile
from pathlib import Path


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("compiler", type=Path)
    parser.add_argument("wasmtime", type=Path)
    parser.add_argument("qemu", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[3]
    fixture = root / "internal/testing/e2ecompiler/self_host_checker_codes_test.go"
    cases = re.findall(r'\{"(view-(?:memo|leaf)-[^"]+)", `([^`]+)`\}', fixture.read_text())
    local_fixture = root / "internal/testing/e2ecompiler/self_host_typed_view_escape_test.go"
    cases.extend((name, source) for name, source in re.findall(
        r'\{"(prepared-[^"]+)", `([^`]+)`, (?:true|false)\}', local_fixture.read_text()))
    if len(cases) != 10:
        raise ValueError("expected ten diagnostic cases")
    paths = {"compiler": args.compiler.resolve(), "wasmtime": args.wasmtime.resolve(),
             "qemu": args.qemu.resolve(), "collector": Path(__file__).resolve(), "fixture": fixture, "local_fixture": local_fixture}
    for directory in [root / "compiler", root / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root))] = path
    hashes = {name: sha(path) for name, path in paths.items()}
    samples, binary_hashes = [], {}
    targets = {"native": ("x86-64-linux", []), "wasm": ("wasm32-wasi", [str(paths["wasmtime"])]),
               "arm64": ("arm64-linux", [str(paths["qemu"])])}
    with tempfile.TemporaryDirectory(prefix="fern-view-checker-") as temporary:
        for label, (target, runner) in targets.items():
            binary = Path(temporary) / label
            built = subprocess.run([str(paths["compiler"]), "-target", target, "-sanitize", "-o", str(binary),
                                    str(root / "compiler/drivers/checker_codes_run.fern")], cwd=root,
                                   text=True, capture_output=True, timeout=120)
            if built.returncode:
                raise ValueError("driver compile failed: " + built.stderr[-1000:])
            binary_hashes[label] = sha(binary)
            for name, source in cases:
                result = subprocess.run([*runner, str(binary)], input=source, text=True, capture_output=True,
                                        env={**os.environ, "FERN_LEAKCHECK": "1"}, timeout=90)
                wanted = 1 if name in ["view-memo-recursive-local", "view-memo-generic-instantiations", "prepared-chain-local", "view-leaf-generic-local"] else 0
                census = re.search(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", result.stderr)
                if (result.returncode != wanted or ("E063\t" in result.stdout) != bool(wanted)
                        or not census or census[1] != census[2] or census[3] != "0" or int(census[1]) == 0):
                    raise ValueError(f"{label}/{name}: {result.returncode} {result.stdout[-500:]} {result.stderr[-1000:]}")
                samples.append({"target": label, "case": name, "source_sha256": hashlib.sha256(source.encode()).hexdigest(),
                                "exit_code": result.returncode, "stdout": result.stdout, "stderr": result.stderr})
                print(label, name, "PASS", census[0], flush=True)
    if hashes != {name: sha(path) for name, path in paths.items()}:
        raise ValueError("sources or tools changed")
    record = {"source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(paths["compiler"]), "-version"], text=True).strip(),
              "runner_versions": {k: subprocess.check_output([str(paths[k]), "--version"], text=True).splitlines()[0]
                                  for k in ["wasmtime", "qemu"]}, "sha256": hashes, "driver_sha256": binary_hashes,
              "samples": samples, "limits": {"compile_seconds": 120, "execution_seconds": 90, "concurrency": 1},
              "limitations": "Finite frontend diagnostics. ARM64 uses QEMU, not the Go ARM harness. No CPU, peak live bytes, RSS, compatibility, or production qualification."}
    args.output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
