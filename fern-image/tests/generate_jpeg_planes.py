"""Generate finite raw-component observations with the offline public C API."""

import argparse
import gzip
import hashlib
import json
from pathlib import Path
import platform
import re
import subprocess
import sys


def sha(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=Path)
    parser.add_argument("--oracle", type=Path, required=True)
    parser.add_argument("--bundle", type=Path, required=True)
    parser.add_argument("--fern-fixtures", type=Path)
    parser.add_argument("--source-reference", type=Path, default=Path(__file__).parent / "fixtures/jpeg-grayscale-reference.json.gz")
    args = parser.parse_args()
    source_fixtures = Path(__file__).parent / "fixtures/jpeg_entropy.fern"
    paths = {"generator": Path(__file__).resolve(), "Python": Path(sys.executable).resolve(),
             "oracle": args.oracle.resolve(), "oracle_source": Path(__file__).with_name("reference_jpeg_planes.c"),
             "source_bundle": args.source_reference.resolve(), "source_entropy_fixtures": source_fixtures.resolve()}
    hashes = {name: sha(path.read_bytes()) for name, path in paths.items()}
    source = json.loads(gzip.decompress(args.source_reference.read_bytes()))
    args.directory.mkdir(parents=True, exist_ok=True)
    cases = []
    by_hash = {}
    for old in source["cases"]:
        encoded = bytes.fromhex(old["encoded_hex"])
        path = args.directory / (old["name"] + ".jpg")
        path.write_bytes(encoded)
        result = subprocess.run([str(args.oracle.resolve()), str(path)], capture_output=True, timeout=20)
        (args.directory / (old["name"] + ".stdout")).write_bytes(result.stdout)
        (args.directory / (old["name"] + ".stderr")).write_bytes(result.stderr)
        if result.returncode or result.stderr:
            raise ValueError("raw-component reference failed: " + old["name"])
        reference = json.loads(result.stdout)
        components = reference["planes"]
        if len(components) != old["components"] or len(components) not in (1, 3):
            raise ValueError("reference component count")
        max_h = max(component["horizontal"] for component in components)
        max_v = max(component["vertical"] for component in components)
        for component in components:
            width = (reference["width"] * component["horizontal"] + max_h - 1) // max_h
            height = (reference["height"] * component["vertical"] + max_v - 1) // max_v
            if component["width"] != width or component["height"] != height or len(component["pixels"]) != width * height:
                raise ValueError("reference component shape")
            if not all(type(value) is int and 0 <= value <= 255 for value in component["pixels"]):
                raise ValueError("reference component sample range")
        entry = {"name": old["name"], "encoded_hex": encoded.hex(), "input_sha256": sha(encoded),
                 "oracle_stdout_sha256": sha(result.stdout), "reference": reference}
        cases.append(entry)
        by_hash[sha(encoded)] = entry
    if hashes != {name: sha(path.read_bytes()) for name, path in paths.items()}:
        raise ValueError("reference source or tool changed")
    document = {"schema": 1, "reference": {"codec": "libjpeg-turbo 2.1.5 JDCT_ISLOW raw components", "Python": platform.python_version(),
                "sha256": hashes, "scope": "Finite raw component planes, not chroma reconstruction, color interpretation, photographs, broad compatibility, security, performance, physical bounds, or production."},
                "cases": cases}
    (args.directory / "reference.json").write_text(json.dumps(document, indent=2) + "\n")
    args.bundle.parent.mkdir(parents=True, exist_ok=True)
    args.bundle.write_bytes(gzip.compress(json.dumps(document, separators=(",", ":")).encode(), mtime=0))
    if args.fern_fixtures:
        text = 'import "std/hex";\n\nfunction bytes(text: string): u8[] {\n  return match (hex.decode(text)) { Ok(v) => v, Err(_) => [] };\n}\n\n'
        for name in ["restart_gray", "restart_rgb444", "restart_rgb420", "arbitrary_identifiers", "fractional_sampling", "single_sampling"]:
            encoded = re.search(r'pub function ' + name + r'\(\): u8\[\] \{\s*return bytes\("([0-9a-f]+)"\)', source_fixtures.read_text()).group(1)
            reference = by_hash[sha(bytes.fromhex(encoded))]["reference"]
            text += "pub function " + name + "(component: i32): u8[] {\n  return match (component) {\n"
            for i, component in enumerate(reference["planes"]):
                text += f'    {i} => bytes("{bytes(component["pixels"]).hex()}"),\n'
            text += "    _ => [],\n  };\n}\n\n"
        args.fern_fixtures.write_text(text)
    print(f"created {len(cases)} inputs and {sum(len(case['reference']['planes']) for case in cases)} component planes")


if __name__ == "__main__":
    main()
