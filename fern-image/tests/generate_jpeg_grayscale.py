"""Store finite whole-image grayscale observations from the offline reference."""

import argparse
import gzip
import hashlib
import json
from pathlib import Path
import platform
import subprocess
import sys


def sha(data):
    return hashlib.sha256(data).hexdigest()


def marker_at(data, wanted):
    at = 2
    while at + 4 <= len(data):
        if data[at] != 255:
            raise ValueError("invalid reference marker boundary")
        marker = data[at + 1]
        if marker == wanted:
            return at
        if marker == 218:
            break
        length = int.from_bytes(data[at + 2:at + 4], "big")
        if length < 2:
            raise ValueError("invalid reference marker length")
        at += length + 2
    raise ValueError("reference marker missing")


def input_cases(original):
    cases = list(original)
    for name, identifier in [("L-0-17x9-o0-r1", 1), ("L-0-35x33-o1-r3", 201)]:
        source = next(case for case in original if case["name"] == name)
        data = bytearray.fromhex(source["encoded_hex"])
        sof, sos = marker_at(data, 192), marker_at(data, 218)
        if data[sof + 9] != 1 or data[sos + 4] != 1:
            raise ValueError("grayscale profile control is not single-component")
        data[sof + 10], data[sof + 11], data[sos + 5] = identifier, 0x44, identifier
        changed = dict(source)
        changed.update(name=f"sampling4x4-id{identifier}-{name}", encoded_hex=data.hex(),
                       input_control={"source": name, "sampling": "4x4", "identifier": identifier})
        cases.append(changed)
    source = next(case for case in original if case["name"] == "L-0-17x9-o1-r3")
    data = bytearray.fromhex(source["encoded_hex"])
    at = 2
    while at + 4 <= len(data):
        marker = data[at + 1]
        length = int.from_bytes(data[at + 2:at + 4], "big")
        if marker == 219:
            cursor = at + 4
            while cursor < at + 2 + length:
                if data[cursor] != 0:
                    raise ValueError("unexpected grayscale quantizer selector")
                data[cursor] = 3
                cursor += 65
        elif marker == 196:
            cursor = at + 4
            while cursor < at + 2 + length:
                if data[cursor] & 15:
                    raise ValueError("unexpected grayscale Huffman selector")
                data[cursor] |= 1
                cursor += 17 + sum(data[cursor + 1:cursor + 17])
        elif marker == 192:
            data[at + 12] = 3
        elif marker == 218:
            data[at + 6] = 17
            break
        at += length + 2
    changed = dict(source)
    changed.update(name="table-selectors-L-0-17x9-o1-r3", encoded_hex=data.hex(),
                   input_control={"source": source["name"], "quantizer": 3, "dc_huffman": 1, "ac_huffman": 1})
    cases.append(changed)
    return cases


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=Path)
    parser.add_argument("--oracle", type=Path, required=True)
    parser.add_argument("--coefficient-reference", type=Path, default=Path(__file__).parent / "fixtures/jpeg-coefficients-reference.json.gz")
    parser.add_argument("--bundle", type=Path, required=True)
    args = parser.parse_args()
    args.directory.mkdir(parents=True, exist_ok=True)
    tools = {"generator": Path(__file__).resolve(), "python": Path(sys.executable).resolve(),
             "oracle": args.oracle.resolve(), "oracle_source": Path(__file__).with_name("reference_jpeg_idct.c"),
             "coefficient_bundle": args.coefficient_reference.resolve()}
    hashes = {name: sha(path.read_bytes()) for name, path in tools.items()}
    original = json.loads(gzip.decompress(args.coefficient_reference.read_bytes()))
    cases = []
    for old in input_cases(original["cases"]):
        encoded = bytes.fromhex(old["encoded_hex"])
        path = args.directory / (old["name"] + ".jpg")
        path.write_bytes(encoded)
        components = len(old["reference"]["components"])
        if components not in (1, 3):
            raise ValueError("unexpected coefficient reference profile")
        entry = {"name": old["name"], "encoded_hex": encoded.hex(), "input_sha256": sha(encoded),
                 "status": "accepted" if components == 1 else "unsupported", "components": components}
        if "input_control" in old:
            entry["input_control"] = old["input_control"]
        if components == 1:
            result = subprocess.run([str(args.oracle.resolve()), str(path)], capture_output=True, timeout=20)
            (args.directory / (old["name"] + ".stdout")).write_bytes(result.stdout)
            (args.directory / (old["name"] + ".stderr")).write_bytes(result.stderr)
            if result.returncode != 0 or result.stderr:
                raise ValueError("offline grayscale observation failed for " + old["name"])
            reference = json.loads(result.stdout)
            width, height = reference["width"], reference["height"]
            if width != old["reference"]["width"] or height != old["reference"]["height"] or len(reference["pixels"]) != width * height:
                raise ValueError("reference dimensions disagree")
            if not all(type(value) is int and 0 <= value <= 255 for value in reference["pixels"]):
                raise ValueError("reference pixel range")
            entry["reference"] = reference
            entry["oracle_stdout_sha256"] = sha(result.stdout)
        cases.append(entry)
    if hashes != {name: sha(path.read_bytes()) for name, path in tools.items()}:
        raise ValueError("reference source or tool changed")
    document = {"schema": 1, "reference": {"codec": "libjpeg-turbo 2.1.5 JDCT_ISLOW grayscale",
                "Python": platform.python_version(), "sha256": hashes,
                "source_encoder": {key: original["manifest"][key] for key in ["Pillow", "encoder", "Python"]},
                "scope": "Finite generated grayscale pixel observations and explicit RGB-profile rejection controls. No photograph, broad compatibility, managed color, performance, memory, or production qualification."},
                "cases": cases}
    (args.directory / "reference.json").write_text(json.dumps(document, indent=2) + "\n")
    args.bundle.parent.mkdir(parents=True, exist_ok=True)
    args.bundle.write_bytes(gzip.compress(json.dumps(document, separators=(",", ":")).encode(), mtime=0))
    print(f"created {len(cases)} cases: {sum(case['status'] == 'accepted' for case in cases)} accepted, {sum(case['status'] == 'unsupported' for case in cases)} unsupported")


if __name__ == "__main__":
    main()
