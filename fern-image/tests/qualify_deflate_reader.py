"""Compare finite raw DEFLATE streams with the offline Python zlib reference."""

import argparse
import contextlib
import hashlib
import json
import os
import platform
import random
import re
import shutil
import subprocess
import sys
import tempfile
import zlib
from pathlib import Path


LIMIT = 131072
SEED = 0xF3A128


def sha(data):
    return hashlib.sha256(data).hexdigest()


def reference(data):
    decoder = zlib.decompressobj(-15)
    try:
        raw = decoder.decompress(data, LIMIT + 1)
        if len(raw) > LIMIT:
            return None
        if not decoder.eof:
            return None
        return raw, len(data) - len(decoder.unused_data)
    except zlib.error:
        return None


def corpus():
    rng = random.Random(SEED)
    cases = []

    def add(name, data):
        if len(data) > 200000:
            raise ValueError("input limit exceeded")
        cases.append({"name": name, "data": data, "expected": reference(data)})

    for size in [0, 1, 2, 3, 255, 256, 257, 258, 259, 32767, 32768, 32769, 65539]:
        for pattern in ["zero", "periodic", "noise"]:
            raw = bytes(rng.randrange(256) if pattern == "noise" else
                        (i * 37 + i // 251) % 256 if pattern == "periodic" else 0 for i in range(size))
            for level in [0, 1, 6, 9]:
                for strategy in [zlib.Z_DEFAULT_STRATEGY, zlib.Z_FIXED]:
                    encoder = zlib.compressobj(level, zlib.DEFLATED, -15, strategy=strategy)
                    encoded = encoder.compress(raw) + encoder.flush()
                    assert reference(encoded) == (raw, len(encoded))
                    add(f"zlib-{size}-{pattern}-{level}-{strategy}", encoded)
    raw = bytes((i * 37 + i // 251) % 256 for i in range(32768))
    distant = b"\0\0\x80\xff\x7f" + raw + bytes([3, 222, 255, 15, 0])
    assert reference(distant) == (raw + raw[:3], len(distant))
    add("manual-distance-32768", distant)
    add("manual-distance-with-trailing", distant + b"trailing bytes")
    add("empty-chain", b"\0\0\0\xff\xff" * 600 + b"\3\0")
    encoder = zlib.compressobj(6, zlib.DEFLATED, -15)
    split = encoder.compress(b"before flush" * 300) + encoder.flush(zlib.Z_SYNC_FLUSH)
    split += encoder.compress(b"after flush" * 300) + encoder.flush()
    add("flush-and-cross-block-history", split)
    add("trailing-stream", split + split)
    encoder = zlib.compressobj(9, zlib.DEFLATED, -15)
    add("output-limit", encoder.compress(b"x" * (LIMIT + 1)) + encoder.flush())
    for name, encoded, expected in [
        ("literal-only", "05c001040000000010000000000000000001000000000000000000000000000000000000000000008010", b"AAA"),
        ("empty-distance", "05c001040000000010000000000000000000000000000000000000000000000000000000000000008000", b""),
        ("empty-distance-many", "05c201040000000010000000000000000001000000000000000000000000000000000000000000008010", b"A"),
        ("invalid-single-literal", "0580010400000040000000000000000000000000000000000000000000000000000000000000000002", None),
        ("invalid-single-distance", "05c001040000008020000000000000000000000000000000000000000000000000000000000000008006", None),
        ("absent-distance-use", "0dc0010400000080200000000000000000010000000000000000000000000000000000000000000000cf", None),
        ("oversubscribed-literal", "05c001040000000010000000000000000003000000000000000000000000000000000000000000008002", None),
        ("oversubscribed-distance", "05c201040000000010000000000000000000000000000000000000000000000000000000000000008007", None),
        ("missing-end", "05c001040000000010000000000000000003000000000000000000000000000000000000000000000000", None),
    ]:
        data = bytes.fromhex(encoded)
        decoded = reference(data)
        assert (decoded[0] if decoded else None) == expected
        add(name, data)
    base = next(c["data"] for c in cases if c["name"] == "zlib-32769-periodic-6-0")
    for end in range(len(base)):
        add(f"prefix-{end}", base[:end])
    for index in range(len(base)):
        data = bytearray(base)
        data[index] ^= 1 << (index % 8)
        add(f"bit-{index}", bytes(data))
    for data in [b"\7", b"\1\1\0\0\0*", b"\1\2\0\xfd\xff*", b"\3\3"]:
        add("invalid-" + data.hex(), data)
    return cases


@contextlib.contextmanager
def workspace(output, start, batch):
    with tempfile.TemporaryDirectory() as temporary:
        directory = Path(temporary)
        try:
            yield directory
        except BaseException:
            failure = Path(tempfile.mkdtemp(prefix=output.stem + "-failure-", dir=output.resolve().parent))
            shutil.copytree(directory, failure, dirs_exist_ok=True)
            manifest = [{"index": start + i, "name": c["name"], "input_sha256": sha(c["data"])} for i, c in enumerate(batch)]
            (failure / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
            print("failure fixtures: " + str(failure), file=sys.stderr)
            raise


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("output", type=Path)
    target = parser.add_mutually_exclusive_group()
    target.add_argument("--wasmtime", type=Path)
    target.add_argument("--qemu", type=Path)
    options = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    binary = options.binary.resolve()
    compiler = options.compiler.resolve()
    paths = {"binary": binary, "compiler": compiler, "collector": Path(__file__),
             "python": Path(sys.executable).resolve(),
             "worker": root / "tests/deflate_reader_corpus.fern",
             "script": root / "scripts/test-deflate-reader.sh"}
    if getattr(zlib, "__file__", None):
        paths["python_zlib"] = Path(zlib.__file__).resolve()
    for directory in [root.parent / "internal/stdlib", root.parent / "compiler"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix == ".fern" or path.name.endswith(".fern.md")):
                paths[str(path.relative_to(root.parent))] = path
    runner = []
    runtime = "native x86-64"
    if options.wasmtime:
        paths["runner"] = options.wasmtime.resolve()
        runner = [str(paths["runner"]), "run", "--dir=."]
        runtime = subprocess.check_output([str(paths["runner"]), "--version"], text=True).strip()
    elif options.qemu:
        paths["runner"] = options.qemu.resolve()
        runner = [str(paths["runner"])]
        runtime = subprocess.check_output([str(paths["runner"]), "--version"], text=True).splitlines()[0]
    hashes = {name: sha(path.read_bytes()) for name, path in paths.items()}
    cases = corpus()
    samples, batches = [], []
    environment = {**os.environ, "FERN_LEAKCHECK": "1"}
    for start in range(0, len(cases), 8):
        batch = cases[start:start + 8]
        with workspace(options.output, start, batch) as directory:
            names = [f"case-{start + i}.bin" for i in range(len(batch))]
            for name, case in zip(names, batch):
                (directory / name).write_bytes(case["data"])
            result = subprocess.run([*runner, str(binary), *names], cwd=directory, env=environment,
                                    text=True, capture_output=True, timeout=60)
            if result.returncode != 0:
                (directory / "worker.stdout.txt").write_text(result.stdout)
                (directory / "worker.stderr.txt").write_text(result.stderr)
                raise ValueError(f"worker exit {result.returncode}: {result.stderr[-1000:]}")
            census = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", result.stderr)
            if not census or census[1] != census[2]:
                raise ValueError("invalid worker leak census")
            lines = result.stdout.splitlines()
            if len(lines) != len(batch):
                raise ValueError("invalid worker output count")
            batches.append({"first": start, "count": len(batch), "allocations": int(census[1]), "exit_code": result.returncode})
            for i, (name, case, line) in enumerate(zip(names, batch, lines)):
                fields = dict(field.split("=", 1) for field in line.split())
                expected = case["expected"]
                if fields.get("index") != str(i) or fields.get("status") != ("ok" if expected else "error"):
                    raise ValueError("reference outcome mismatch: " + case["name"] + ": " + line)
                sample = {"name": case["name"], "input_sha256": sha(case["data"]), "input_bytes": len(case["data"]), **fields}
                if expected is not None:
                    raw, consumed = expected
                    if (directory / (name + ".raw")).read_bytes() != raw or int(fields["bytes"]) != len(raw) or int(fields["consumed"]) != consumed:
                        raise ValueError("reference bytes or consumption mismatch: " + case["name"])
                    sample["output_sha256"] = sha(raw)
                if (directory / name).read_bytes() != case["data"]:
                    raise ValueError("input changed")
                samples.append(sample)
        print(f"passed {min(start + 8, len(cases))}/{len(cases)}", flush=True)
    if hashes != {name: sha(path.read_bytes()) for name, path in paths.items()}:
        raise ValueError("sources or tools changed")
    record = {"source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
              "compiler_version": subprocess.check_output([str(compiler), "-version"], text=True).strip(),
              "host": platform.platform(), "runtime": runtime, "seed": SEED, "sha256": hashes,
              "reference": {"Python": platform.python_version(), "zlib": zlib.ZLIB_RUNTIME_VERSION},
              "limits": {"input_bytes": 200000, "output_bytes": LIMIT, "history_bytes": 32768,
                         "chunk_sizes": [1, 7, 257, 4096, 32768], "cases_per_process": 8,
                         "process_deadline_seconds": 60, "concurrency": 1},
              "compiler_flags": ["-target", "wasm32-wasi" if options.wasmtime else "arm64-linux" if options.qemu else "x86-64-linux", "-sanitize"], "warmup_processes": 0,
              "cache_policy": "No page-cache flush or cold/warm performance comparison.",
              "limitations": "Finite generated raw-stream qualification. Whole encoded input and caller-retained comparison bytes remain live. History length is not allocator capacity, peak live bytes, RSS, or a process bound. Error priority can differ between eager and resumable decoders. No zlib/gzip wrapper streaming, PNG integration, CPU deadline, compatibility parity, performance, or production claim.",
              "samples": samples, "batches": batches}
    options.output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
