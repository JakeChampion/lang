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
from fixtures.generate_png_packed import vector as packed_vector
from fixtures.generate_png_indexed import vector as indexed_vector
from fixtures.generate_png_transparency import vector as transparency_vector


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
        if image.mode not in ["1", "L", "LA", "P", "RGB", "RGBA"]:
            raise ValueError("reference mode is outside the supported surface")
        if "transparency" in image.info and image.mode in ["1", "L", "RGB"]:
            depth = data[24]
            stored = next(payload for kind, payload in chunks(data) if kind == b"tRNS")
            key = struct.unpack(">" + "H" * (len(stored) // 2), stored)
            if image.mode == "RGB":
                image.info["transparency"] = tuple(value & 255 for value in key)
            else:
                key = key[0] & ((1 << depth) - 1)
                # Pillow expands packed gray samples but keeps the unscaled key.
                image.info["transparency"] = key * 255 // ((1 << depth) - 1)
            image = image.convert("RGBA")
        if image.mode in ["1", "L", "LA"]:
            image = image.convert("RGBA" if image.mode == "LA" else "RGB")
        elif image.mode == "P":
            image = image.convert("RGBA" if "transparency" in image.info else "RGB")
        return image.size, image.mode, image.tobytes()


def corpus():
    rng = random.Random(SEED)
    cases = []

    def add(name, data, expected=None):
        if len(data) > 65536:
            raise ValueError("campaign input limit exceeded")
        cases.append({"name": name, "data": data, "expected": expected})

    dimensions = [(1, 1), (1, 31), (31, 1), (2, 3), (3, 2), (7, 9), (16, 17), (31, 29)]
    for depth, color in [(1, 0), (2, 0), (4, 0), (8, 0), (8, 2)]:
        maximum = (1 << depth) - 1
        for width in [1, 7, 8, 9, 17, 129, 257]:
            for key in sorted({0, maximum // 2, maximum}):
                for padding in [0, 1]:
                    for high in [False, True]:
                        data, raw, normalized = transparency_vector(depth, color, width, key, padding, high)
                        expected = ((width, 5), "RGBA", raw)
                        if reference(data) != expected or reference(normalized) != expected:
                            raise ValueError("sample transparency reference differs")
                        add(f"sample-key-{color}-{depth}-{width}-{key}-pad{padding}-high{high}", data, expected)
        data, _, _ = transparency_vector(depth, color, 17, maximum // 2)
        header, trns, idat, iend = chunks(data)
        for length in range(9):
            if length != len(trns[1]):
                add(f"sample-key-{color}-{depth}-length-{length}", assemble([header, (b"tRNS", bytes(length)), idat, iend]))
        for label, altered in {
            "duplicate": [header, trns, trns, idat, iend],
            "after-data": [header, idat, trns, iend],
            "before-header": [trns, header, idat, iend],
            "palette-after-key": [header, trns, (b"PLTE", bytes([19, 27, 43])), idat, iend],
        }.items():
            add(f"sample-key-{color}-{depth}-{label}", assemble(altered))
        for label, stream in [("short", zlib.decompress(idat[1])[:-1]), ("long", zlib.decompress(idat[1]) + b"\0")]:
            add(f"sample-key-{color}-{depth}-{label}", assemble([header, trns, (b"IDAT", zlib.compress(stream)), iend]))
        bad_filter = bytearray(zlib.decompress(idat[1]))
        bad_filter[4 * (len(bad_filter) // 5)] = 5
        for checksum in [False, True]:
            compressed = bytearray(zlib.compress(bad_filter))
            if checksum:
                compressed[-1] ^= 1
            add(f"sample-key-{color}-{depth}-late-filter-checksum{checksum}", assemble([header, trns, (b"IDAT", compressed), iend]))
        if color == 2:
            add("sample-key-suggested-palette", assemble([header, (b"PLTE", bytes([19, 27, 43])), trns, idat, iend]), reference(data))
    for width, depth, color in [(8193, 8, 2), (8193, 8, 0), (8193, 4, 0)]:
        data, raw, _ = transparency_vector(depth, color, width, 0, high=True)
        add(f"sample-key-boundary-{color}-{depth}", data, ((width, 5), "RGBA", raw))
    for depth in [1, 2, 4, 8]:
        for width in [1, 7, 8, 9, 17, 129, 257]:
            for padding in [0, 1]:
                for alpha_count in [-1, 0, 1, 1 << depth]:
                    data, raw, channels = indexed_vector(depth, width, padding, alpha_count)
                    expected = ((width, 5), "RGBA" if channels == 4 else "RGB", raw)
                    if reference(data) != expected:
                        raise ValueError("indexed reference differs")
                    add(f"indexed-{depth}-{width}-pad{padding}-alpha{alpha_count}", data, expected)
        data, raw, channels = indexed_vector(depth, 17, 1, 0, entries=1)
        add(f"indexed-{depth}-single-opaque-entry", data, ((17, 5), "RGBA", raw))
        bad_index, _, _ = indexed_vector(depth, 17, 1, 1, entries=1, bad_index=True)
        add(f"indexed-{depth}-late-bad-index", bad_index)
        bad_parts = chunks(bad_index)
        corrupt = bytearray(bad_parts[-2][1])
        corrupt[-1] ^= 1
        add(f"indexed-{depth}-checksum-before-bad-index", assemble([*bad_parts[:-2], (b"IDAT", corrupt), bad_parts[-1]]))
        data, _, _ = indexed_vector(depth, 17, 1, 1)
        parts = chunks(data)
        ihdr, plte, trns, idat, iend = parts
        invalid_chunks = {
            "missing-palette": [ihdr, trns, idat, iend],
            "missing-palette-opaque": [ihdr, idat, iend],
            "duplicate-palette": [ihdr, plte, plte, trns, idat, iend],
            "palette-after-alpha": [ihdr, plte, trns, plte, idat, iend],
            "palette-after-data": [ihdr, plte, idat, plte, iend],
            "alpha-before-palette": [ihdr, trns, plte, idat, iend],
            "alpha-after-data": [ihdr, plte, idat, trns, iend],
            "duplicate-alpha": [ihdr, plte, trns, trns, idat, iend],
            "excess-alpha": [ihdr, plte, (b"tRNS", bytes((1 << depth) + 1)), idat, iend],
        }
        for size in [0, 1, 2, 4, 769, 771]:
            invalid_chunks[f"palette-length-{size}"] = [ihdr, (b"PLTE", bytes(size)), idat, iend]
        if depth != 8:
            invalid_chunks["palette-exceeds-depth"] = [ihdr, (b"PLTE", bytes(3 * ((1 << depth) + 1))), idat, iend]
        for label, altered in invalid_chunks.items():
            add(f"indexed-{depth}-{label}", assemble(altered))
    for width, height in [(8193, 5)]:
        data, raw, channels = indexed_vector(8, width, 1, 1, height=height)
        add(f"indexed-chunk-boundary-{width}x{height}", data, ((width, height), "RGBA", raw))
    for depth in [1, 2, 4]:
        for width in [1, 7, 8, 9, 17, 33, 129]:
            for padding in [0, 1]:
                data, raw = packed_vector(depth, width, padding)
                expected = ((width, 5), "RGB", raw)
                if reference(data) != expected:
                    raise ValueError("packed grayscale reference differs")
                add(f"packed-gray-{depth}-{width}-padding{padding}", data, expected)
        data, _ = packed_vector(depth, 17, 1)
        parts = chunks(data)
        for bad_depth in [0, 3, 16]:
            altered = bytearray(parts[0][1])
            altered[8] = bad_depth
            add(f"packed-gray-{depth}-invalid-depth-{bad_depth}", assemble([(b"IHDR", altered), *parts[1:]]))
        altered = bytearray(parts[0][1])
        altered[9] = 4
        add(f"packed-gray-{depth}-invalid-alpha", assemble([(b"IHDR", altered), *parts[1:]]))
        raw_rows = zlib.decompress(parts[1][1])
        bad_filter = bytearray(raw_rows)
        bad_filter[4 * (len(raw_rows) // 5)] = 5
        for label, filtered in [("late-filter", bad_filter), ("short", raw_rows[:-1]), ("long", raw_rows + b"\0")]:
            add(f"packed-gray-{depth}-{label}", assemble([parts[0], (b"IDAT", zlib.compress(filtered)), parts[-1]]))
    for mode in ["RGB", "RGBA", "L", "LA"]:
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
                    expanded = image.convert("RGB" if mode == "L" else "RGBA") if mode in ["L", "LA"] else image
                    expected = ((width, height), expanded.mode, expanded.tobytes())
                    if reference(data) != expected:
                        raise ValueError("reference encoder changed the source pixels")
                    add(f"pillow-{mode}-{width}x{height}-{pattern}-level{level}", data, expected)

    # Each forced filter stream also passes the independent reference decoder.
    for mode in ["RGB", "RGBA", "L", "LA"]:
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
            parts = [(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, {"L": 0, "LA": 4, "RGB": 2, "RGBA": 6}[mode], 0, 0, 0)),
                     (b"IDAT", zlib.compress(filtered)), (b"IEND", b"")]
            data = assemble(parts)
            expanded = Image.frombytes(mode, (width, height), raw)
            if mode in ["L", "LA"]:
                expanded = expanded.convert("RGB" if mode == "L" else "RGBA")
            expected = ((width, height), expanded.mode, expanded.tobytes())
            if reference(data) != expected:
                raise ValueError("forced filter fixture disagrees with Pillow")
            add(f"filter-{mode}-{filter_id}", data, expected)

    base = next(case for case in cases if case["name"] == "pillow-RGBA-7x9-noise-level6")
    parts = chunks(base["data"])
    compressed = b"".join(data for kind, data in parts if kind == b"IDAT")
    header = parts[0]
    add("suggested-palette-RGBA", assemble([header, (b"PLTE", bytes([19, 27, 43])), *parts[1:]]), base["expected"])
    for step in [1, 7, 31]:
        split = [(b"IDAT", b"")] + [(b"IDAT", compressed[i:i + step]) for i in range(0, len(compressed), step)]
        add(f"split-idat-{step}", assemble([header, *split, (b"IDAT", b""), (b"IEND", b"")]), base["expected"])

    for mode in ["P", "I;16"]:
        output = io.BytesIO()
        Image.new(mode, (3, 2)).save(output, format="PNG")
        add(("pillow-" if mode == "P" else "unsupported-") + mode, output.getvalue(), reference(output.getvalue()) if mode == "P" else None)
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
    target.add_argument("--native-target", choices=["x86-64-linux", "arm64-darwin"])
    parser.add_argument("--max-cases", type=int)
    options = parser.parse_args()
    if options.max_cases is not None and options.max_cases <= 0:
        parser.error("--max-cases must be positive")
    root = Path(__file__).resolve().parents[1]
    binary, compiler = options.binary.resolve(), options.compiler.resolve()
    paths = {"binary": binary, "compiler": compiler, "collector": Path(__file__),
             "python": Path(sys.executable).resolve()}
    if getattr(zlib, "__file__", None):
        paths["python_zlib"] = Path(zlib.__file__)
    paths["fern-image/tests/png_corpus.fern"] = root / "tests/png_corpus.fern"
    paths["fern-image/tests/fixtures/generate_png_packed.py"] = root / "tests/fixtures/generate_png_packed.py"
    paths["fern-image/tests/fixtures/generate_png_indexed.py"] = root / "tests/fixtures/generate_png_indexed.py"
    paths["fern-image/tests/fixtures/generate_png_transparency.py"] = root / "tests/fixtures/generate_png_transparency.py"
    paths["fern-image/tests/requirements-reference.txt"] = root / "tests/requirements-reference.txt"
    paths["fern-image/scripts/test-png-corpus.sh"] = root / "scripts/test-png-corpus.sh"
    for directory in [root / "packages", root.parent / "internal/stdlib", Path(PIL.__file__).parent]:
        for path in sorted(directory.rglob("*")):
            if path.is_file() and (path.suffix in [".fern", ".py", ".so"] or path.name.endswith(".fern.md")):
                key = str(path.relative_to(root.parent)) if path.is_relative_to(root.parent) else "reference/PIL/" + str(path.relative_to(directory))
                paths[key] = path
    for directory in Path(PIL.__file__).parent.parent.glob("*illow.libs"):
        for path in sorted(directory.rglob("*")):
            if path.is_file():
                paths["reference/" + directory.name + "/" + str(path.relative_to(directory))] = path
    runner = []
    native_target = options.native_target or "x86-64-linux"
    runtime = "native " + native_target
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
    if options.max_cases is not None:
        cases = cases[:options.max_cases]
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
                    rows = (directory / (name + ".rows.raw")).read_bytes()
                    selected = (directory / (name + ".selected.raw")).read_bytes()
                    incremental_rows = (directory / (name + ".incremental.rows.raw")).read_bytes()
                    incremental_selected = (directory / (name + ".incremental.selected.raw")).read_bytes()
                    first, count = size[1] // 2, min(2, size[1] - size[1] // 2)
                    stride = size[0] * len(mode)
                    if (decoded != raw or rows != raw or incremental_rows != raw
                            or selected != raw[first * stride:(first + count) * stride]
                            or incremental_selected != selected or reference(encoded) != expected):
                        raise ValueError("pixel mismatch in " + case["name"])
                    crop_x = size[0] // 2
                    crop_width = min(2, size[0] - crop_x)
                    crop = b"".join(raw[y * stride + crop_x * len(mode):
                                        y * stride + (crop_x + crop_width) * len(mode)]
                                    for y in range(first, first + count))
                    for api in ["legacy", "fused", "incremental.legacy", "incremental.fused"]:
                        graph = (directory / (name + "." + api + ".graph.raw")).read_bytes()
                        tile = (directory / (name + "." + api + ".tile.raw")).read_bytes()
                        if graph != raw or tile != crop:
                            raise ValueError("provider pixel mismatch in " + case["name"] + "/" + api)
                        sample[api.replace(".", "_") + "_graph_pixel_sha256"] = sha(graph)
                        sample[api.replace(".", "_") + "_tile_pixel_sha256"] = sha(tile)
                    sample["crop"] = {"x": crop_x, "y": first, "width": crop_width, "height": count}
                    if (int(fields["width"]), int(fields["height"])) != size or int(fields["channels"]) != len(mode):
                        raise ValueError("metadata mismatch in " + case["name"])
                    if (int(fields["first"]), int(fields["count"])) != (first, count):
                        raise ValueError("row selection mismatch in " + case["name"])
                    sample.update({"pixel_sha256": sha(raw), "row_pixel_sha256": sha(rows), "selected_pixel_sha256": sha(selected), "encoded_sha256": sha(encoded), "encoded_bytes": len(encoded)})
                    sample.update({"incremental_row_pixel_sha256": sha(incremental_rows),
                                   "incremental_selected_pixel_sha256": sha(incremental_selected)})
                elif any(fields.get(api) != fields.get("error") for api in
                         ["row_error", "provider_error", "incremental_error", "incremental_provider_error"]):
                    raise ValueError("row or provider rejection mismatch in " + case["name"])
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
        "compiler_flags": ["-target", "wasm32-wasi" if options.wasmtime else "arm64-linux" if options.qemu else native_target, "-sanitize"],
        "max_cases": options.max_cases,
        "warmup_processes": 0, "cache_policy": "No page-cache flush or cold/warm performance comparison.",
        "row_checks": {"accepted": "Full rows and a middle selection match independent reference bytes.",
                       "rejected": "Eager, row, and graph provider APIs return the same codec error class.",
                       "filtered_frame": "The eager row API materializes the frame before callbacks. The incremental API validates the complete stream before a second decode pass."},
        "incremental_checks": {"rows": "Full rows and a middle selection match the independent reference.",
                               "providers": "Legacy/fused graph rows and cropped tiles match the independent reference.",
                               "retention": "No complete filtered frame in the decoder session. Encoded input and comparison outputs remain retained. No physical memory bound follows."},
        "provider_checks": {"graph": "Two identity nearest nodes preserve independent reference bytes through legacy and fused row execution.",
                            "tile": "Middle cropped tiles match independent reference bytes through legacy and fused execution.",
                            "retention": "The worker retains output bytes for comparison. The graph plan excludes caller retention."},
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
