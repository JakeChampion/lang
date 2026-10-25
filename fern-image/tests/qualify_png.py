"""Compare a bounded PNG corpus with an offline Pillow reference."""

import argparse
import contextlib
import hashlib
import io
import json
import platform
import random
import re
import shutil
import struct
import subprocess
import sys
import tempfile
import zlib
from pathlib import Path

import PIL
from PIL import Image, features


SEED = 0xF3A123
SIGNATURE = b"\x89PNG\r\n\x1a\n"


def sha(data):
    return hashlib.sha256(data).hexdigest()


def chunk(kind, data):
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))


def chunks(data):
    result = []
    offset = 8
    while offset < len(data):
        size = struct.unpack_from(">I", data, offset)[0]
        result.append((data[offset + 4:offset + 8], data[offset + 8:offset + 8 + size]))
        offset += size + 12
    return result


def assemble(parts):
    return SIGNATURE + b"".join(chunk(kind, data) for kind, data in parts)


def reference(data):
    with Image.open(io.BytesIO(data)) as image:
        image.load()
        if image.mode not in ["RGB", "RGBA"]:
            raise ValueError("reference mode is outside the supported surface")
        return image.size, image.mode, image.tobytes()


def corpus():
    rng = random.Random(SEED)
    cases = []

    def add(name, data, expected=None):
        if len(data) > 65536:
            raise ValueError("campaign input limit exceeded")
        cases.append({"name": name, "data": data, "expected": expected})

    dimensions = [(1, 1), (1, 31), (31, 1), (2, 3), (3, 2), (7, 9), (16, 17), (31, 29)]
    for mode in ["RGB", "RGBA"]:
        for width, height in dimensions:
            for pattern in ["gradient", "noise"]:
                count = width * height * len(mode)
                raw = bytes(rng.randrange(256) if pattern == "noise" else
                            (i * 37 + i // len(mode) * 11) % 256 for i in range(count))
                image = Image.frombytes(mode, (width, height), raw)
                for level in [0, 1, 6, 9]:
                    output = io.BytesIO()
                    image.save(output, format="PNG", compress_level=level)
                    data = output.getvalue()
                    expected = ((width, height), mode, raw)
                    if reference(data) != expected:
                        raise ValueError("reference encoder changed the source pixels")
                    add(f"pillow-{mode}-{width}x{height}-{pattern}-level{level}", data, expected)

    # Each forced filter stream also passes the independent reference decoder.
    for mode in ["RGB", "RGBA"]:
        width, height, bpp = 17, 9, len(mode)
        raw = bytes(rng.randrange(256) for _ in range(width * height * bpp))
        for filter_id in range(5):
            filtered = bytearray()
            stride = width * bpp
            for y in range(height):
                filtered.append(filter_id)
                for x in range(stride):
                    at = y * stride + x
                    a = raw[at - bpp] if x >= bpp else 0
                    b = raw[at - stride] if y else 0
                    c = raw[at - stride - bpp] if y and x >= bpp else 0
                    p = a + b - c
                    distances = [abs(p - a), abs(p - b), abs(p - c)]
                    paeth = [a, b, c][distances.index(min(distances))]
                    prediction = [0, a, b, (a + b) // 2, paeth][filter_id]
                    filtered.append((raw[at] - prediction) % 256)
            parts = [(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2 if bpp == 3 else 6, 0, 0, 0)),
                     (b"IDAT", zlib.compress(filtered)), (b"IEND", b"")]
            data = assemble(parts)
            expected = ((width, height), mode, raw)
            if reference(data) != expected:
                raise ValueError("forced filter fixture disagrees with Pillow")
            add(f"filter-{mode}-{filter_id}", data, expected)

    base = next(case for case in cases if case["name"] == "pillow-RGBA-7x9-noise-level6")
    parts = chunks(base["data"])
    compressed = b"".join(data for kind, data in parts if kind == b"IDAT")
    header = parts[0]
    for step in [1, 7, 31]:
        split = [(b"IDAT", b"")] + [(b"IDAT", compressed[i:i + step]) for i in range(0, len(compressed), step)]
        add(f"split-idat-{step}", assemble([header, *split, (b"IDAT", b""), (b"IEND", b"")]), base["expected"])

    for mode in ["L", "LA", "P", "I;16"]:
        output = io.BytesIO()
        Image.new(mode, (3, 2)).save(output, format="PNG")
        add("unsupported-" + mode, output.getvalue())
    ihdr = header[1]
    for position, value in [(8, 16), (9, 3), (10, 1), (11, 1), (12, 1)]:
        altered = bytearray(ihdr)
        altered[position] = value
        add(f"unsupported-header-{position}", assemble([(b"IHDR", altered), *parts[1:]]))
    for width, height in [(0, 9), (7, 0), (0x80000000, 9), (7, 0xffffffff), (65536, 65536), (65537, 1)]:
        add(f"dimension-{width}x{height}", assemble([(b"IHDR", struct.pack(">II", width, height) + ihdr[8:]), *parts[1:]]))
    for kind, data in [(b"tRNS", b"\0" * 6), (b"iCCP", b"x\0\0x"), (b"gAMA", struct.pack(">I", 45455)),
                       (b"cHRM", b"\0" * 32), (b"acTL", struct.pack(">II", 1, 0)), (b"ABCD", b""),
                       (b"vpAG", b"allowed ancillary"), (b"vpaG", b""), (b"vp1G", b"")]:
        data = assemble([header, (kind, data), *parts[1:]])
        add("chunk-" + kind.decode(), data, base["expected"] if kind == b"vpAG" else None)
    add("duplicate-header", assemble([header, *parts]))
    add("missing-idat", assemble([header, (b"IEND", b"")]))
    add("missing-iend", assemble(parts[:-1]))
    add("nonempty-iend", assemble([*parts[:-1], (b"IEND", b"x")]))
    add("trailing-file-byte", base["data"] + b"\0")
    add("nonconsecutive-idat", assemble([header, (b"IDAT", compressed[:3]), (b"vpAG", b""),
                                          (b"IDAT", compressed[3:]), (b"IEND", b"")]))
    for name, stream in [("empty", b""), ("trailing", compressed + b"x"), ("second-frame", compressed * 2),
                         ("short-rows", zlib.compress(b"\0" * 10)), ("long-rows", zlib.compress(b"\0" * 1000)),
                         ("bad-filter", zlib.compress(b"\5" + b"\0" * 28 + (b"\0" * 29) * 8)),
                         ("expansion", zlib.compress(b"\0" * 1048576))]:
        add("zlib-" + name, assemble([header, (b"IDAT", stream), (b"IEND", b"")]))
    for i in range(len(base["data"])):
        add(f"prefix-{i}", base["data"][:i])
        mutated = bytearray(base["data"])
        mutated[i] ^= 1 << (i % 8)
        add(f"byte-bit-{i}", bytes(mutated))
    for i in range(128):
        mutated = bytearray(compressed)
        offset = rng.randrange(len(mutated))
        mutated[offset] ^= 1 << rng.randrange(8)
        data = assemble([header, (b"IDAT", mutated), (b"IEND", b"")])
        expected = None
        try:
            inflater = zlib.decompressobj()
            raw = inflater.decompress(mutated, 4096)
            if inflater.eof and not inflater.unused_data and len(raw) == 29 * 9:
                expected = reference(data)
        except (zlib.error, OSError, ValueError):
            pass
        add(f"deflate-bit-{i}", data, expected)
    return cases


@contextlib.contextmanager
def workspace(output, cases):
    with tempfile.TemporaryDirectory() as temporary:
        directory = Path(temporary)
        try:
            yield directory
        except BaseException:
            failure = Path(tempfile.mkdtemp(prefix=output.stem + "-failure-", dir=output.resolve().parent))
            shutil.copytree(directory, failure, dirs_exist_ok=True)
            manifest = [{"index": i, "name": c["name"], "input_sha256": sha(c["data"])} for i, c in enumerate(cases)]
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
    binary, compiler = options.binary.resolve(), options.compiler.resolve()
    paths = {"binary": binary, "compiler": compiler, "collector": Path(__file__),
             "python": Path(sys.executable).resolve()}
    if getattr(zlib, "__file__", None):
        paths["python_zlib"] = Path(zlib.__file__)
    paths["fern-image/tests/png_corpus.fern"] = root / "tests/png_corpus.fern"
    paths["fern-image/tests/requirements-reference.txt"] = root / "tests/requirements-reference.txt"
    paths["fern-image/scripts/test-png-corpus.sh"] = root / "scripts/test-png-corpus.sh"
    for directory in [root / "packages", root.parent / "internal/stdlib", Path(PIL.__file__).parent]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and path.suffix in [".fern", ".py", ".so"]:
                key = str(path.relative_to(root.parent)) if path.is_relative_to(root.parent) else "reference/PIL/" + str(path.relative_to(directory))
                paths[key] = path
    for directory in Path(PIL.__file__).parent.parent.glob("*illow.libs"):
        for path in sorted(directory.rglob("*")):
            if path.is_file():
                paths["reference/" + directory.name + "/" + str(path.relative_to(directory))] = path
    runner = []
    runtime = "native x86-64"
    if options.wasmtime:
        wasmtime = options.wasmtime.resolve()
        paths["wasmtime"] = wasmtime
        runner = [str(wasmtime), "run", "--dir=."]
        runtime = subprocess.check_output([str(wasmtime), "--version"], text=True).strip()
    elif options.qemu:
        qemu = options.qemu.resolve()
        paths["qemu"] = qemu
        runner = [str(qemu)]
        runtime = subprocess.check_output([str(qemu), "--version"], text=True).splitlines()[0]
    hashes = {name: sha(path.read_bytes()) for name, path in paths.items()}
    cases = corpus()
    samples = []
    batch_records = []
    with workspace(options.output, cases) as directory:
        for start in range(0, len(cases), 16):
            batch = cases[start:start + 16]
            names = []
            for i, case in enumerate(batch):
                name = f"case-{start + i}.png"
                (directory / name).write_bytes(case["data"])
                names.append(name)
            result = subprocess.run([*runner, str(binary), *names], cwd=directory, text=True,
                                    capture_output=True, timeout=20, check=True)
            lines = result.stdout.splitlines()
            if len(lines) != len(batch):
                raise ValueError("unexpected worker output count")
            leak = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", result.stderr)
            if not leak or leak[1] != leak[2]:
                raise ValueError("worker allocations remain live")
            batch_records.append({"first": start, "count": len(batch), "allocations": int(leak[1]), "exit_code": result.returncode})
            for i, (case, name, line) in enumerate(zip(batch, names, lines)):
                fields = dict(field.split("=", 1) for field in line.split())
                expected = case["expected"]
                if fields.get("index") != str(i) or fields.get("status") != ("ok" if expected else "error"):
                    raise ValueError(f"case {case['name']}: unexpected result {line}")
                sample = {"name": case["name"], "input_sha256": sha(case["data"]), "input_bytes": len(case["data"]),
                          "expected_status": "ok" if expected else "error", **fields}
                if expected:
                    size, mode, raw = expected
                    decoded = (directory / (name + ".raw")).read_bytes()
                    encoded = (directory / (name + ".encoded.png")).read_bytes()
                    if decoded != raw or reference(encoded) != expected:
                        raise ValueError("pixel mismatch in " + case["name"])
                    if (int(fields["width"]), int(fields["height"])) != size or int(fields["channels"]) != len(mode):
                        raise ValueError("metadata mismatch in " + case["name"])
                    sample.update({"pixel_sha256": sha(raw), "encoded_sha256": sha(encoded), "encoded_bytes": len(encoded)})
                if (directory / name).read_bytes() != case["data"]:
                    raise ValueError("input fixture changed")
                samples.append(sample)
            for path in directory.iterdir():
                path.unlink()
            print(f"passed {min(start + 16, len(cases))}/{len(cases)}", flush=True)
    if hashes != {name: sha(path.read_bytes()) for name, path in paths.items()}:
        raise ValueError("sources or tools changed")
    record = {
        "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
        "compiler_version": subprocess.check_output([str(compiler), "-version"], text=True).strip(),
        "host": platform.platform(), "host_machine": platform.machine(), "runtime": runtime, "seed": SEED,
        "compiler_flags": ["-target", "wasm32-wasi" if options.wasmtime else "arm64-linux" if options.qemu else "x86-64-linux", "-sanitize"],
        "warmup_processes": 0, "cache_policy": "No page-cache flush or cold/warm performance comparison.",
        "reference": {"Pillow": PIL.__version__, "Pillow_zlib": features.version_codec("zlib"),
                      "Python": platform.python_version(), "Python_zlib": zlib.ZLIB_RUNTIME_VERSION,
                      "Python_zlib_module": zlib.__spec__.origin}, "sha256": hashes,
        "limits": {"input_bytes": 65536, "pixels": 65536, "decoded_bytes": 262144,
                   "encoded_bytes": 1048576, "cases_per_process": 16, "process_deadline_seconds": 20, "concurrency": 1},
        "provenance": "Algorithmic gradients and seeded noise, with generated PNG structures. No external source artwork. The repository does not assign a new dataset license. Photographs and licensed external datasets remain unqualified.",
        "limitations": "Finite deterministic qualification, not an unbounded fuzz campaign or a broad compatibility claim. Pillow and Python zlib are offline references only. No throughput, visual-quality, process memory bound, aggregate service, or production-safety claim. Native sanitization and Wasm leak census have different capabilities.",
        "samples": samples, "batches": batch_records,
    }
    options.output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
