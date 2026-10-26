"""Generate finite JPEG header fixtures with the pinned offline reference."""

import argparse
import hashlib
import io
import json
from pathlib import Path

import PIL
from PIL import Image, features


def segment(marker, payload):
    return bytes([255, marker]) + (len(payload) + 2).to_bytes(2, "big") + payload


def synthetic():
    quant = segment(219, bytes([0]) + bytes(range(1, 65)))
    counts = bytes([1] + [0] * 15)
    codes = segment(196, bytes([0]) + counts + bytes([0]) + bytes([16]) + counts + bytes([0]))
    frame = segment(192, bytes([8, 0, 9, 0, 17, 1, 1, 17, 0]))
    scan = segment(218, bytes([1, 1, 0, 0, 63, 0]))
    return bytes([255, 216]) + quant + codes + frame + scan


def entropy_offset(data):
    at = 2
    while at < len(data):
        assert data[at] == 255
        marker = data[at + 1]
        length = int.from_bytes(data[at + 2:at + 4], "big")
        assert length >= 2
        at += length + 2
        if marker == 218:
            return at
    raise AssertionError("missing scan")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    assert PIL.__version__ == "12.3.0", "use tests/requirements-reference.txt"
    root = Path(__file__).resolve().parent
    entries = []
    fixture = ['import "std/hex";\n',
               'function bytes(text: string): u8[] {\n  return match (hex.decode(text)) { Ok(value) => value, Err(_) => [] };\n}\n']
    configs = [("L", 0, "gray", False, False, (17, 9)),
               ("RGB", 0, "rgb444", False, False, (17, 9)),
               ("RGB", 1, "rgb422", False, False, (17, 9)),
               ("RGB", 2, "rgb420", False, False, (17, 9)),
               ("L", 0, "gray_optimized", True, False, (1, 1)),
               ("RGB", 2, "rgb420_optimized", True, False, (17, 9)),
               ("RGB", 2, "progressive", False, True, (17, 9)),
               ("CMYK", 0, "cmyk", False, False, (3, 2))]
    for mode, sampling, name, optimize, progressive, size in configs:
        image = Image.new(mode, size)
        image.putdata([(0, 32, 64, 17) if mode == "CMYK" else
                       (x * 17 + y * 23) % 256 if mode == "L" else
                       ((x * 17 + y * 7) % 256, (x * 31 + y * 13) % 256, (x * 5 + y * 29) % 256)
                       for y in range(size[1]) for x in range(size[0])])
        stream = io.BytesIO()
        image.save(stream, format="JPEG", quality=83, subsampling=sampling, progressive=progressive, optimize=optimize)
        data = stream.getvalue()
        reference = Image.open(io.BytesIO(data))
        reference.load()
        assert reference.size == size and reference.mode == mode
        entries.append({"name": name, "sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data),
                        "width": size[0], "height": size[1], "components": reference.layer,
                        "optimized": optimize, "progressive": progressive,
                        "accepted_header_profile": not progressive and mode != "CMYK",
                        "entropy_offset": entropy_offset(data)})
        fixture.append(f'pub function {name}(): u8[] {{\n  return bytes("{data.hex()}");\n}}\n')
    fixture.append(f'pub function synthetic_header(): u8[] {{\n  return bytes("{synthetic().hex()}");\n}}\n')
    manifest = {"Pillow": PIL.__version__, "jpeg_api_version": Image.core.jpeglib_version,
                "libjpeg_turbo_version": features.version_feature("libjpeg_turbo"),
                "generator_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                "reference_use": "Offline header fixtures only. No runtime codec dependency or decoder compatibility claim.",
                "fixtures": entries,
                "synthetic_scope": "Header only, without entropy data or EOI. Not a complete JPEG fixture."}
    outputs = {root / "fixtures/jpeg_headers.fern": "\n".join(fixture),
               root / "fixtures/jpeg_headers.json": json.dumps(manifest, indent=2) + "\n"}
    for path, content in outputs.items():
        if args.check:
            assert path.read_text() == content, str(path) + " differs"
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)


if __name__ == "__main__":
    main()
