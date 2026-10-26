"""Create finite entropy fixtures for the offline coefficient oracle."""

from pathlib import Path
import argparse
import hashlib
import gzip
import platform
import sys
import io
import json
import subprocess

import PIL
from PIL import Image, features


def segment(marker, data):
    return bytes([255, marker]) + (len(data) + 2).to_bytes(2, "big") + data


def synthetic_header(width, height, restart=0, components=None, scan=None, dc_symbols=(0, 1, 2), ac_symbols=(0, 1, 240)):
    components = components or [(1, 1, 1)]
    scan = scan or [c[0] for c in components]
    quant = segment(219, bytes([0]) + bytes([1] * 64))
    dc = bytes([0, 3] + [0] * 14) + bytes(dc_symbols)
    ac = bytes([0, 0, 3] + [0] * 13) + bytes(ac_symbols)
    codes = segment(196, bytes([0]) + dc + bytes([16]) + ac)
    frame = segment(192, bytes([8]) + height.to_bytes(2, "big") + width.to_bytes(2, "big") + bytes([len(components)])
                    + b"".join(bytes([c, h * 16 + v, 0]) for c, h, v in components))
    start = segment(218, bytes([len(scan)]) + b"".join(bytes([c, 0]) for c in scan) + bytes([0, 63, 0]))
    return bytes([255, 216]) + quant + codes + frame + (segment(221, restart.to_bytes(2, "big")) if restart else b"") + start


def packed(bits):
    bits += "1" * (-len(bits) % 8)
    raw = bytes(int(bits[i:i + 8], 2) for i in range(0, len(bits), 8))
    return raw.replace(b"\xff", b"\xff\0")


def cases():
    boundary = "01" + "1" * 10 + "001" + "1" * 10 + "001" + "0" * 10 + "000"
    boundary += "10" + "0" * 11 + "000" + "10" + "1" * 11 + "000"
    yield "category-boundaries", synthetic_header(24, 8, dc_symbols=(0, 10, 11), ac_symbols=(0, 10, 240)) + packed(boundary) + b"\xff\xd9"
    # DC -2, AC +1 at zig-zag 1, 16 zeros, AC -1 at zig-zag 18, EOB.
    signed = "10" + "01" + "001" + "1" + "010" + "001" + "0" + "000"
    yield "signed-zigzag", synthetic_header(8, 8) + packed(signed) + b"\xff\xd9"
    yield "predictor", synthetic_header(16, 8) + packed(signed + "10" + "10" + "000") + b"\xff\xd9"
    data = synthetic_header(8, 80, 1)
    for i in range(10):
        data += packed("10" + ("01" if i % 2 == 0 else "10") + "000")
        if i != 9:
            data += bytes([255, 208 + i % 8])
    yield "restart-cycle", data + b"\xff\xd9"
    yield "last-coefficient", synthetic_header(8, 8) + packed("00" + ("0011" * 63)) + b"\xff\xd9"
    yield "last-zrl", synthetic_header(8, 8) + packed("00" + ("0011" * 47) + "010") + b"\xff\xd9"
    yield "arbitrary-identifiers", synthetic_header(8, 8, components=[(17, 1, 1), (201, 1, 1), (55, 1, 1)]) + packed("1001000" + "1010000" + "010000") + b"\xff\xd9"
    yield "sampling-3-2-1", synthetic_header(17, 9, components=[(7, 3, 1), (2, 2, 1), (9, 1, 1)]) + packed("00000" * 12) + b"\xff\xd9"
    yield "single-sampling-4x4", synthetic_header(17, 9, components=[(1, 4, 4)]) + packed("00000" * 6) + b"\xff\xd9"
    for mode in ["L", "RGB"]:
        for sampling in ([0] if mode == "L" else [0, 1, 2]):
            for width, height in [(1, 1), (8, 8), (17, 9), (35, 33)]:
                for optimize in [False, True]:
                    for restart in [0, 1, 3]:
                        image = Image.new(mode, (width, height))
                        image.putdata([((x * 37 + y * 71 + (x ^ y) * 13) % 256 if mode == "L" else
                                        ((x * 17 + y * 7) % 256, (x * 31 + y * 13) % 256, (x * 5 + y * 29) % 256))
                                       for y in range(height) for x in range(width)])
                        stream = io.BytesIO()
                        image.save(stream, format="JPEG", quality=83, subsampling=sampling, optimize=optimize, restart_marker_blocks=restart)
                        name = f"{mode}-{sampling}-{width}x{height}-o{int(optimize)}-r{restart}"
                        yield name, stream.getvalue()


def reference_paths(oracle):
    paths = {"python": Path(sys.executable).resolve(), "oracle": oracle.resolve(),
             "generator": Path(__file__).resolve(),
             "oracle_source": Path(__file__).with_name("reference_jpeg_coefficients.c")}
    package = Path(PIL.__file__).parent
    for path in sorted(package.rglob("*")):
        if path.is_file() and path.suffix in (".py", ".so"):
            paths["PIL/" + str(path.relative_to(package))] = path
    for directory in package.parent.glob("*illow.libs"):
        for path in sorted(directory.rglob("*")):
            if path.is_file():
                paths[directory.name + "/" + str(path.relative_to(directory))] = path
    return paths


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=Path)
    parser.add_argument("--oracle", type=Path, required=True)
    parser.add_argument("--fixture", type=Path)
    parser.add_argument("--bundle", type=Path)
    args = parser.parse_args()
    assert PIL.__version__ == "12.3.0"
    args.directory.mkdir(parents=True, exist_ok=True)
    tools = reference_paths(args.oracle)
    tool_hashes = {name: hashlib.sha256(path.read_bytes()).hexdigest() for name, path in tools.items()}
    entries = []
    bundled = []
    fixtures = ['import "std/hex";\n', 'function bytes(text: string): u8[] { return match (hex.decode(text)) { Ok(v) => v, Err(_) => [] }; }\n']
    selected = {"category-boundaries": "category_boundaries", "signed-zigzag": "signed", "predictor": "predictor", "restart-cycle": "restart_cycle",
                "last-coefficient": "last_coefficient", "last-zrl": "last_zrl", "arbitrary-identifiers": "arbitrary_identifiers",
                "sampling-3-2-1": "fractional_sampling", "single-sampling-4x4": "single_sampling",
                "RGB-2-35x33-o1-r3": "restart_rgb420", "RGB-0-17x9-o0-r1": "restart_rgb444", "L-0-17x9-o1-r3": "restart_gray"}
    for name, data in cases():
        path = args.directory / (name + ".jpg")
        path.write_bytes(data)
        run = subprocess.run([str(args.oracle), str(path)], capture_output=True, check=True)
        reference = json.loads(run.stdout)
        bundled.append({"name": name, "encoded_hex": data.hex(), "reference": reference,
                        "oracle_stdout_sha256": hashlib.sha256(run.stdout).hexdigest()})
        assert not run.stderr, run.stderr
        (args.directory / (name + ".json")).write_text(json.dumps(reference, separators=(",", ":")) + "\n")
        entries.append({"name": name, "sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data),
                        "blocks": sum(len(c["blocks"]) for c in reference["components"]),
                        "oracle_stdout_sha256": hashlib.sha256(run.stdout).hexdigest()})
        if name in selected:
            fixtures.append(f'pub function {selected[name]}(): u8[] {{ return bytes("{data.hex()}"); }}\n')
    if tool_hashes != {name: hashlib.sha256(path.read_bytes()).hexdigest() for name, path in tools.items()}:
        raise ValueError("reference source or tool changed")
    manifest = {"reference_files_sha256": tool_hashes, "Pillow": PIL.__version__, "encoder": features.version_feature("libjpeg_turbo"),
                "oracle_sha256": hashlib.sha256(args.oracle.read_bytes()).hexdigest(),
                "generator_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                "oracle_source_sha256": hashlib.sha256(Path(__file__).with_name("reference_jpeg_coefficients.c").read_bytes()).hexdigest(),
                "Python": platform.python_version(), "cases": entries}
    (args.directory / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    if args.fixture:
        args.fixture.parent.mkdir(parents=True, exist_ok=True)
        args.fixture.write_text("\n".join(fixtures))
    if args.bundle:
        args.bundle.parent.mkdir(parents=True, exist_ok=True)
        document = {"schema": 1, "manifest": manifest, "cases": bundled}
        args.bundle.write_bytes(gzip.compress(json.dumps(document, separators=(",", ":")).encode(), mtime=0))
    print(f"Created {len(entries)} accepted coefficient references.")


if __name__ == "__main__":
    main()
