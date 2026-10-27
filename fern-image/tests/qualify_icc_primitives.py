"""Record one frozen-profile primitive probe without claiming ICC parser conformance."""

import argparse
from decimal import Decimal, localcontext
import hashlib
import importlib
import json
import os
from pathlib import Path
import platform
import re
import struct
import subprocess
import sys

from PIL import ImageCms, features
import PIL


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--mode", choices=["native", "wasm", "arm64"], required=True)
    parser.add_argument("--runner", type=Path)
    parser.add_argument("--oracle", type=Path)
    options = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    fixture = root / "tests/fixtures/icc-feasibility-srgb.icc"
    manifest = root / "tests/fixtures/icc-feasibility-reference.json"
    expected = json.loads(manifest.read_text())
    data = fixture.read_bytes()
    if sha(fixture) != expected["sha256"]:
        raise ValueError("frozen profile identity mismatch")
    profile = ImageCms.ImageCmsProfile(str(fixture))
    if (ImageCms.getProfileName(profile).strip() != expected["name"]
            or ImageCms.getProfileCopyright(profile).strip() != expected["copyright"]):
        raise ValueError("independent profile metadata mismatch")
    tags = [dict(signature=data[132 + i * 12:136 + i * 12].decode("ascii"),
                 offset=struct.unpack_from(">I", data, 136 + i * 12)[0],
                 size=struct.unpack_from(">I", data, 140 + i * 12)[0])
            for i in range(struct.unpack_from(">I", data, 128)[0])]
    if tags != expected["tags"]:
        raise ValueError("frozen tag directory mismatch")
    with localcontext() as context:
        context.prec = 90
        gamma = Decimal(struct.unpack_from(">i", data, 532)[0]) / 65536
        power = (Decimal("0.5").ln() * gamma).exp()
        matrix = [sum((Decimal(struct.unpack_from(">i", data, off + 8 + 4 * row)[0]) / 65536) * weight
                      for off, weight in zip([460, 500, 480], [Decimal(".25"), Decimal(".5"), Decimal(".75")], strict=True))
                  for row in range(3)]
    compiler = options.compiler.resolve()
    binary = root / ("build/icc-primitives-" + options.mode + (".wasm" if options.mode == "wasm" else ""))
    paths = dict(compiler=compiler, Python=Path(sys.executable).resolve(), fixture=fixture, reference=manifest,
                 probe=root / "experiments/icc-primitives.fern", collector=Path(__file__).resolve(),
                 script=root / "scripts/check-icc-primitives.sh", workflow=root.parent / ".github/workflows/fern-image.yml")
    for directory in [root.parent / "compiler", root.parent / "internal/stdlib"]:
        for p in sorted(directory.rglob("*.fern")):
            paths[str(p.relative_to(root.parent))] = p
        for p in sorted(directory.rglob("*.fern.md")):
            paths[str(p.relative_to(root.parent))] = p
    for name in ["PIL.ImageCms", "PIL.Image", "PIL._imagingcms", "decimal", "_decimal", "struct", "_struct"]:
        module = importlib.import_module(name)
        if getattr(module, "__file__", None):
            paths[name] = Path(module.__file__).resolve()
    command = []
    if options.mode != "native":
        if options.runner is None:
            raise ValueError("execution runner is required")
        paths["runner"] = options.runner.resolve()
        command = [str(paths["runner"])]
        if options.mode == "wasm":
            command += ["run", "--dir=."]
    if options.oracle:
        paths["Go_oracle"] = options.oracle.resolve()
    hashes = {name: sha(path) for name, path in paths.items()}
    target = {"native": "x86-64-linux", "wasm": "wasm32-wasi", "arm64": "arm64-linux"}[options.mode]
    options.output.parent.mkdir(parents=True, exist_ok=True)
    prefix = options.output.with_suffix("")
    record = dict(status="running", mode=options.mode, source_commit=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                  worktree_dirty=bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True)),
                  compiler_version=subprocess.check_output([str(compiler), "-version"], text=True).strip(),
                  Pillow=PIL.__version__, LittleCMS=features.version_module("littlecms2"), Python=platform.python_version(),
                  reference=expected, Decimal_precision=90, reference_power=str(power), reference_matrix=[str(v) for v in matrix],
                  power_sample_absolute_tolerance=1e-12, sha256=hashes,
                  scope="One fixed-profile byte/arithmetic primitive probe, not an ICC parser, full curve evaluator, transform, accuracy bound, or conformance result.")
    try:
        with Path(str(prefix) + ".compile.log").open("w") as log:
            result = subprocess.run([str(compiler), "-target", target, "-sanitize", "-o", str(binary), "experiments/icc-primitives.fern"],
                                    cwd=root, stdout=log, stderr=subprocess.STDOUT)
        record["compile_exit"] = result.returncode
        if result.returncode:
            raise ValueError("primitive compilation failed")
        paths["probe_binary"] = binary
        hashes["probe_binary"] = sha(binary)
        result = subprocess.run([*command, str(binary)], cwd=root, env=dict(os.environ, FERN_LEAKCHECK="1"),
                                capture_output=True, text=True, timeout=20)
        Path(str(prefix) + ".stdout.txt").write_text(result.stdout)
        Path(str(prefix) + ".stderr.txt").write_text(result.stderr)
        record["run_exit"] = result.returncode
        heap = re.findall(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", result.stderr)
        if (result.returncode or result.stdout != "ICC primitive probe: 588 bytes, 11 tags, 3 curve aliases\n"
                or "fern-sanitizer:" in result.stderr or len(heap) != 1):
            raise ValueError("primitive execution or output failed")
        a, f, live = map(int, heap[0])
        if a <= 0 or a != f or live != 0:
            raise ValueError("unbalanced or empty primitive heap census")
        record["heap"] = dict(allocs=a, frees=f, live_bytes=live)
        if options.oracle:
            with Path(str(prefix) + ".oracle.log").open("w") as log:
                checked = subprocess.run([str(paths["Go_oracle"]), "-check", "experiments/icc-primitives.fern"], cwd=root, stdout=log, stderr=subprocess.STDOUT)
                interpreted = subprocess.run([str(paths["Go_oracle"]), "-interp", "experiments/icc-primitives.fern"], cwd=root, stdout=log, stderr=subprocess.STDOUT)
            record["Go"] = dict(check_exit=checked.returncode, interp_exit=interpreted.returncode)
            if checked.returncode or interpreted.returncode:
                raise ValueError("Go oracle probe failed")
        record["sources_unchanged"] = hashes == {name: sha(path) for name, path in paths.items()}
        if not record["sources_unchanged"]:
            raise ValueError("source or tool changed")
        record["status"] = "complete"
    except BaseException as error:
        record["status"] = "failed"
        record["failure"] = str(error)
        if isinstance(error, subprocess.TimeoutExpired):
            for suffix, value in [(".stdout.txt", error.stdout), (".stderr.txt", error.stderr)]:
                if value is not None:
                    Path(str(prefix) + suffix).write_bytes(value.encode() if isinstance(value, str) else value)
        record["sources_unchanged"] = hashes == {name: sha(path) for name, path in paths.items()}
        raise
    finally:
        options.output.write_text(json.dumps(record, indent=2) + "\n")
    print(json.dumps(dict(status=record["status"], mode=options.mode, compile_exit=record["compile_exit"],
                          run_exit=record["run_exit"], heap=record["heap"], sources_unchanged=record["sources_unchanged"])))


if __name__ == "__main__":
    main()
