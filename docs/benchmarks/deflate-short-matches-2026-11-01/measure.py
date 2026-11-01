"""Compare native encoders on identical tile and corpus bytes."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import struct
import subprocess
import zlib

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
TILES = [
    ("z0", 0, 0, 0),
    ("z2", 2, 1, 1),
    ("z14", 14, 7193, 7652),
    ("z38", 38, 120695873044, 128379946314),
]


def sha(path):
    return hashlib.file_digest(Path(path).open("rb"), "sha256").hexdigest()


def run(command, **kwargs):
    return subprocess.run(
        [str(arg) for arg in command], capture_output=True, check=True,
        timeout=110, **kwargs,
    )


def decode_png(path):
    data = path.read_bytes()
    assert data[:8] == b"\x89PNG\r\n\x1a\n"
    at, compressed, header, kind = 8, b"", b"", b""
    while at < len(data):
        count = struct.unpack_from(">I", data, at)[0]
        kind, payload = data[at + 4:at + 8], data[at + 8:at + 8 + count]
        assert zlib.crc32(kind + payload) == struct.unpack_from(">I", data, at + 8 + count)[0]
        if kind == b"IHDR":
            header = payload
        if kind == b"IDAT":
            compressed += payload
        at += count + 12
    assert kind == b"IEND" and at == len(data) and header
    return header, zlib.decompress(compressed)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compiler", type=Path, required=True)
    parser.add_argument("--baseline-stdlib", type=Path, required=True)
    parser.add_argument("--candidate-stdlib", type=Path, default=ROOT / "internal/stdlib")
    parser.add_argument("--work", type=Path, required=True)
    parser.add_argument("--runs", type=int, default=5)
    parser.add_argument("--corpus", type=Path)
    parser.add_argument("--corpus-limit", type=int, default=0)
    parser.add_argument("--cbench", type=Path)
    parser.add_argument("--historical", default="6e26931f")
    options = parser.parse_args()
    if options.runs < 1 or options.corpus_limit < 0:
        parser.error("runs must be positive and corpus-limit must be nonnegative")
    targets = {
        ("Darwin", "arm64"): "arm64-darwin",
        ("Linux", "aarch64"): "arm64-linux",
        ("Linux", "x86_64"): "x86-64-linux",
    }
    target = targets.get((platform.system(), platform.machine()))
    if target is None:
        parser.error("this benchmark requires a supported native host")
    compiler = options.compiler.resolve()
    work = options.work.resolve()
    work.mkdir(parents=True, exist_ok=True)
    stdlibs = {
        "baseline": options.baseline_stdlib.resolve(),
        "candidate": options.candidate_stdlib.resolve(),
    }
    binaries, renderers, costs = {}, {}, {}

    def build(source, output, stdlib, instrument=False):
        env = dict(os.environ)
        if instrument:
            env["FERN_LEAKCHECK"] = "1"
        run([compiler, "-target", target, "-O", "-o", output, source, stdlib], env=env)
        return output

    for label, stdlib in stdlibs.items():
        binaries[label] = build(ROOT / "scripts/deflate-bench.d/fbench.fern", work / (label + "-bench"), stdlib)
        renderers[label] = build(HERE / "render.fern", work / (label + "-render"), stdlib)
        costs[label] = build(HERE / "codec_cost.fern", work / (label + "-cost"), stdlib, True)

    historical = run(
        ["git", "show", options.historical + ":internal/stdlib/std/deflate.fern.md"],
        cwd=ROOT,
    ).stdout.decode()
    original_sha = hashlib.sha256(historical.encode()).hexdigest()
    adaptations = [
        ('import "std/error";', 'import "core/error";', 1),
        ('import "std/hash";', 'import "std/hash/crc32";', 1),
        ("hash.crc32_new()", "crc32.new()", 2),
    ]
    for old, new, count in adaptations:
        assert historical.count(old) == count
        historical = historical.replace(old, new)
    (work / "historical.fern.md").write_text(historical)
    source = (ROOT / "scripts/deflate-bench.d/fbench.fern").read_text()
    (work / "historical-bench.fern").write_text(
        source.replace('import "std/deflate";', 'import "./historical" as deflate;')
    )
    binaries["historical"] = build(
        work / "historical-bench.fern", work / "historical-bench",
        stdlibs["candidate"],
    )
    report = {
        "host": {"system": platform.system(), "machine": platform.machine()},
        "target": target,
        "compiler_sha256": sha(compiler),
        "runs_per_measurement": options.runs,
        "candidate_source_sha256": sha(stdlibs["candidate"] / "std/deflate.fern.md"),
        "baseline_source_sha256": sha(stdlibs["baseline"] / "std/deflate.fern.md"),
        "encoder_sha256": {name: sha(path) for name, path in binaries.items()},
        "historical": {
            "commit": options.historical, "original_source_sha256": original_sha,
            "adapted_source_sha256": sha(work / "historical.fern.md"),
            "compatibility_changes": adaptations,
        },
        "png": [], "measurements": [], "allocator": [],
    }
    if options.cbench:
        report["cbench_sha256"] = sha(options.cbench)

    def measure(path, label):
        original = path.read_bytes()
        for name, binary in binaries.items():
            stream = work / (label + "-" + name + ".deflate")
            result = run([binary, "c", path, options.runs, stream])
            assert zlib.decompress(stream.read_bytes(), -15) == original
            fields = {key: int(value) for key, value in re.findall(r"(\w+)=(\d+)", result.stdout.decode())}
            report["measurements"].append({
                "input": label, "encoder": name, **fields,
                "input_sha256": sha(path), "output_sha256": sha(stream),
            })
        if options.cbench:
            stream = work / (label + "-reference.deflate")
            result = run([options.cbench, "c", path, 6, options.runs, stream])
            assert zlib.decompress(stream.read_bytes(), -15) == original
            run([binaries["candidate"], "d", stream, path, options.runs])
            run([options.cbench, "d", work / (label + "-candidate.deflate"), path, options.runs])
            fields = {key: int(value) for key, value in re.findall(r"(\w+)=(\d+)", result.stdout.decode())}
            report["measurements"].append({
                "input": label, "encoder": "reference-level-6", **fields,
                "input_sha256": sha(path), "output_sha256": sha(stream),
            })

    for name, zoom, x, y in TILES:
        decoded = []
        for label, binary in renderers.items():
            image = work / (name + "-" + label + ".png")
            run([binary, zoom, x, y, image])
            header, raw = decode_png(image)
            decoded.append((header, raw))
            report["png"].append({
                "tile": name, "encoder": label, "bytes": image.stat().st_size,
                "crc32": zlib.crc32(image.read_bytes()),
                "filtered_sha256": hashlib.sha256(raw).hexdigest(),
            })
        assert decoded[0] == decoded[1]
        raw_path = work / (name + ".raw")
        raw_path.write_bytes(decoded[0][1])
        measure(raw_path, name)

    gzip_input = work / "gzip-fixture.raw"
    gzip_input.write_bytes(b"".join(
        ("line " + str(i) + " of the corpus\n").encode() for i in range(3000)
    ))
    for label, binary in costs.items():
        for name, path, mode in [("z0", work / "z0.raw", "deflate"), ("gzip-fixture", gzip_input, "gzip")]:
            output = work / (name + "-" + label + ".stream")
            result = run([binary, path, mode, output], env=dict(os.environ, FERN_LEAKCHECK="1"))
            size, events, bump = map(int, result.stdout.splitlines())
            assert zlib.decompress(output.read_bytes(), 31 if mode == "gzip" else -15) == path.read_bytes()
            census = re.search(rb"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", result.stderr)
            assert census and census[1] == census[2] and int(census[3]) == 0
            report["allocator"].append({
                "input": name, "encoder": label, "output_bytes": size,
                "allocation_events_during_encode": events,
                "arena_bump_bytes_during_encode": bump,
            })

    if options.corpus:
        files = sorted(path for path in options.corpus.iterdir() if path.is_file())
        if options.corpus_limit:
            files = files[:options.corpus_limit]
        for path in files:
            measure(path, path.name)
    destination = work / ("report-limit" + str(options.corpus_limit) + "-runs" + str(options.runs) + ".json")
    destination.write_text(json.dumps(report, indent=2) + "\n")
    print(destination)


if __name__ == "__main__":
    main()
