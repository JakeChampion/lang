"""Compare the Fern codec with a pinned offline QOI reference header."""
import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import random
import re
import shutil
import subprocess
import sys
import time

REFERENCE_REVISION = "ffb2d2cb74a1de60819b21b939f7209aa53e91c1"
REFERENCE_SHA256 = "7de6fca1a285b1c20d38f2723dec8b774eb9f144edb9710800a95feeea09375a"


class Descriptor(ctypes.Structure):
    _fields_ = [("width", ctypes.c_uint), ("height", ctypes.c_uint),
                ("channels", ctypes.c_ubyte), ("colorspace", ctypes.c_ubyte)]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def fixtures():
    rng = random.Random(20261107)
    for channels in (3, 4):
        for color in (0, 1):
            for width, height in ((1, 1), (2, 3), (63, 1), (17, 11)):
                for pattern in ("black", "ramp", "random"):
                    size = width * height * channels
                    if pattern == "black":
                        raw = bytes(size)
                    elif pattern == "ramp":
                        raw = bytes((i * 17 + i // channels) % 256 for i in range(size))
                    else:
                        raw = bytes(rng.randrange(256) for _ in range(size))
                    yield Descriptor(width, height, channels, color), raw


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fern", type=Path, required=True)
    parser.add_argument("--reference-header", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--target", choices=("arm64-darwin", "arm64-linux", "x86-64-linux", "wasm32-wasi"), required=True)
    parser.add_argument("--runner", action="append", default=[])
    parser.add_argument("--cc", default="cc")
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[2]
    image = repo / "fern-image"
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    assert digest(args.reference_header) == REFERENCE_SHA256, "reference header hash differs"
    inputs = [image / "packages/codecs/qoi/qoi.fern", image / "packages/pixels/pixels.fern", Path(__file__).resolve()]
    frozen = {str(p): digest(p) for p in inputs}
    compiler_hash = digest(args.fern)
    wrapper = out / "reference.c"
    wrapper.write_text('#define QOI_IMPLEMENTATION\n#define QOI_NO_STDIO\n#include ' + json.dumps(str(args.reference_header.resolve())) + '\nvoid reference_free(void *p) { free(p); }\n')
    library = out / "reference.so"
    subprocess.run([args.cc, "-shared", "-fPIC", "-O2", str(wrapper), "-o", str(library)], check=True)
    ref = ctypes.CDLL(str(library))
    ref.qoi_encode.argtypes = [ctypes.c_void_p, ctypes.POINTER(Descriptor), ctypes.POINTER(ctypes.c_int)]
    ref.qoi_encode.restype = ctypes.c_void_p
    ref.qoi_decode.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.POINTER(Descriptor), ctypes.c_int]
    ref.qoi_decode.restype = ctypes.c_void_p
    ref.reference_free.argtypes = [ctypes.c_void_p]
    modules = out / "source"
    for path in inputs[:2]:
        dest = modules / path.relative_to(image)
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, dest)
    lines = ['import "./packages/codecs/qoi/qoi";', 'import "std/hex";']
    expected = []
    manifest = []
    for i, (desc, raw) in enumerate(fixtures()):
        length = ctypes.c_int()
        pointer = ref.qoi_encode(ctypes.c_char_p(raw), ctypes.byref(desc), ctypes.byref(length))
        assert pointer and length.value > 22
        encoded = ctypes.string_at(pointer, length.value)
        ref.reference_free(pointer)
        expected.append((desc, raw))
        manifest.append(dict(width=desc.width, height=desc.height, channels=desc.channels,
                             colorspace=desc.colorspace, raw_sha256=hashlib.sha256(raw).hexdigest(),
                             encoded_sha256=hashlib.sha256(encoded).hexdigest()))
        array = '[' + ','.join(str(b) for b in encoded) + ']'
        want = '[' + ','.join(str(b) for b in raw) + ']'
        color = "qoi.Srgb" if desc.colorspace == 0 else "qoi.Linear"
        lines += [f'function case{i}(): boolean {{', f'  let input: u8[] = {array};',
                  '  let Ok(decoded) = qoi.decode(input, qoi.default_limits(), (): boolean => false) else { return false; };',
                  f'  let want: u8[] = {want};',
                  f'  if (decoded.image.layout.width != {desc.width} || decoded.image.layout.height != {desc.height} || decoded.color_space != {color} || decoded.image.data.len() != want.len()) {{ return false; }}',
                  '  for j in 0..want.len() { if (decoded.image.data[j] != want[j]) { return false; } }',
                  '  let Ok(encoded) = qoi.encode(decoded.image, decoded.color_space, 10000, (): boolean => false) else { return false; };',
                  '  print(hex.encode(encoded));', '  return true;', '}']
    lines += ['function main(): i32 {']
    lines += [f'  if (!case{i}()) {{ return 1; }}' for i in range(len(expected))]
    lines += ['  return 0;', '}']
    source = modules / "reference.fern"
    source.write_text('\n'.join(lines) + '\n')
    artifact = out / "fern-reference"
    flags = ["-emit", "core-module"] if args.target == "wasm32-wasi" else ["-sanitize"]
    command = [str(args.fern.resolve()), "-target", args.target, *flags, "-o", str(artifact), str(source), str(repo / "internal/stdlib")]
    started = time.monotonic()
    build = subprocess.run(command, cwd=repo, env=dict(os.environ, FERN_LEAKCHECK="1"), capture_output=True, timeout=120)
    (out / "build.stdout").write_bytes(build.stdout)
    (out / "build.stderr").write_bytes(build.stderr)
    build.check_returncode()
    run = subprocess.run([*args.runner, str(artifact)], cwd=repo, capture_output=True, timeout=120)
    (out / "run.stdout").write_bytes(run.stdout)
    (out / "run.stderr").write_bytes(run.stderr)
    run.check_returncode()
    rows = run.stdout.decode().splitlines()
    assert len(rows) == len(expected), "missing or extra image records"
    leak = re.fullmatch(rb'leakcheck: allocs=(\d+) frees=\1 live_bytes=0\n', run.stderr)
    assert leak and int(leak[1]) > 0, run.stderr
    for row, (wanted, raw) in zip(rows, expected):
        encoded = bytes.fromhex(row)
        actual = Descriptor()
        pointer = ref.qoi_decode(ctypes.c_char_p(encoded), len(encoded), ctypes.byref(actual), wanted.channels)
        assert pointer, "reference refuses Fern encoding"
        try:
            assert (actual.width, actual.height, actual.channels, actual.colorspace) == (wanted.width, wanted.height, wanted.channels, wanted.colorspace)
            decoded = ctypes.string_at(pointer, len(raw))
        finally:
            ref.reference_free(pointer)
        assert decoded == raw, "reference pixel mismatch"
    assert all(digest(Path(p)) == value for p, value in frozen.items())
    assert digest(args.fern) == compiler_hash
    record = dict(complete=True, target=args.target, cases=len(expected), source_inventory=frozen,
                  compiler_sha256=compiler_hash, reference_revision=REFERENCE_REVISION,
                  reference_header_sha256=REFERENCE_SHA256, manifest=manifest,
                  python_version=sys.version, reference_library_sha256=digest(library),
                  cc_version=subprocess.check_output([args.cc, "--version"], text=True).splitlines()[0],
                  allocations=int(leak[1]), live_bytes=0, seconds=time.monotonic()-started,
                  scope="Finite bidirectional reference comparison. Exact pixels and declarations. No timing improvement, broad compatibility or process-memory-bound claim.")
    (out / "proof.json").write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps({k: v for k, v in record.items() if k not in ("manifest", "source_inventory")}))


if __name__ == "__main__":
    main()
