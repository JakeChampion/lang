"""Construct finite raw images and independent exact Pillow orientation observations."""

import argparse
import copy
import gzip
import hashlib
import importlib
import json
from pathlib import Path
import platform
import random

from PIL import Image, ImageOps
import PIL


def sha(data):
    return hashlib.sha256(data).hexdigest()


def observed(case):
    c = case["config"]
    mode = "RGB" if c["channels"] == 3 else "RGBA"
    image = Image.frombytes(mode, (c["width"], c["height"]), bytes.fromhex(case["data_hex"]),
                            "raw", mode, c["stride"], 1)
    image.getexif()[274] = c["orientation"]
    output = ImageOps.exif_transpose(image)
    return output.width, output.height, output.tobytes()


def make_case(index, width, height, channels, padding, value, rng, pattern):
    stride = width * channels + padding
    data = bytearray([251] * (stride * height))
    for y in range(height):
        for x in range(width):
            for k in range(channels):
                sample = (y * width * 19 + x * 23 + k * 37) % 256 if pattern == 0 else rng.randrange(256)
                if channels == 4 and k == 3:
                    sample = [0, 1, 127, 128, 254, 255][(x + y) % 6]
                data[y * stride + x * channels + k] = sample
    config = dict(width=width, height=height, stride=stride, claimed_bytes=len(data), channels=channels,
                  orientation=value, max_pixels=width * height, max_bytes=width * height * channels,
                  max_planned_bytes=len(data) + width * height * channels, cancel_at=0)
    case = dict(name=f"orientation-{index:04d}", config=config, data_hex=data.hex(),
                data_sha256=sha(data), pillow=True)
    w, h, raw = observed(case)
    case["output_hex"] = raw.hex()
    case["expected"] = dict(status="ok", width=w, height=h, stride=w * channels, channels=channels,
                            bytes=len(raw), input_bytes=len(data), planned_bytes=len(data) + len(raw),
                            polls=3 + h * ((w + 63) // 64), data_sha256=sha(raw))
    return case


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=Path(__file__).parent / "fixtures/jpeg-orientation-reference.json.gz")
    options = parser.parse_args()
    rng = random.Random(13655)
    modules = ["PIL.Image", "PIL.ImageOps", "PIL.ExifTags", "PIL.TiffImagePlugin", "PIL._imaging", "random"]
    paths = {name: Path(importlib.import_module(name).__file__) for name in modules}
    paths["generator"] = Path(__file__)
    hashes = {name: sha(path.read_bytes()) for name, path in paths.items()}
    cases = []
    dimensions = [(1, 1), (1, 2), (2, 1), (2, 3), (3, 2), (7, 5), (16, 9),
                  (65, 3), (3, 65), (257, 2), (2, 257), (33, 17)]
    for width, height in dimensions:
        for channels in [3, 4]:
            for pattern in [0, 1]:
                for value in range(1, 9):
                    cases.append(make_case(len(cases), width, height, channels, (width + height) % 8,
                                           value, rng, pattern))
    for index in range(16):
        width, height = rng.randrange(1, 32), rng.randrange(1, 32)
        for value in range(1, 9):
            cases.append(make_case(len(cases), width, height, 3 + index % 2, index % 9, value, rng, 1))

    def append_error(base, updates, error, *, data=None, pillow=True):
        case = copy.deepcopy(base)
        case["name"] = f"orientation-{len(cases):04d}"
        case["config"].update(updates)
        if data is not None:
            case["data_hex"] = data.hex()
            case["data_sha256"] = sha(data)
        case["pillow"] = pillow
        case["expected"] = dict(status="error", error=error, polls=0)
        cases.append(case)

    for channels in [3, 4]:
        for value in range(1, 9):
            base = make_case(0, 3, 2, channels, 5, value, rng, 1)
            for updates, error in [({"width": 0}, "Image.Dimensions"),
                                   ({"height": -1}, "Image.Dimensions"),
                                   ({"width": 2147483647}, "Image.Overflow"),
                                   ({"height": 2147483647}, "Image.Overflow"),
                                   ({"stride": channels * 3 - 1}, "Image.Stride"),
                                   ({"claimed_bytes": 1}, "Image.Storage")]:
                append_error(base, updates, error, pillow=False)
            raw = bytes.fromhex(base["data_hex"])
            for data in [raw[:-1], raw + b"\0"]:
                append_error(base, {}, "Image.Storage", data=data, pillow=False)
            for updates, error in [({"max_pixels": 5}, "Image.Limit"),
                                   ({"max_bytes": channels * 6 - 1}, "Image.Limit"),
                                   ({"max_planned_bytes": base["config"]["max_planned_bytes"] - 1}, "Limit"),
                                   ({"max_planned_bytes": 0}, "Limit"),
                                   ({"max_pixels": 0}, "Image.Limit")]:
                append_error(base, updates, error)
            wide = make_case(0, 65, 3, channels, 2, value, rng, 1)
            last = wide["expected"]["polls"]
            for stop in sorted({1, 2, 3, 4, last - 1, last}):
                append_error(wide, {"cancel_at": stop}, "Cancelled")
                cases[-1]["expected"]["polls"] = stop
            complete = copy.deepcopy(wide)
            complete["name"] = f"orientation-{len(cases):04d}"
            complete["config"]["cancel_at"] = last + 1
            cases.append(complete)
    assert len({c["name"] for c in cases}) == len(cases)
    for case in cases:
        if case["pillow"]:
            w, h, raw = observed(case)
            assert raw.hex() == case["output_hex"]
            if case["expected"]["status"] == "ok":
                assert (w, h) == (case["expected"]["width"], case["expected"]["height"])
    assert hashes == {name: sha(path.read_bytes()) for name, path in paths.items()}
    document = dict(schema=1, reference=dict(Pillow=PIL.__version__, Python=platform.python_version(),
                                            seed=13655, sha256=hashes), cases=cases)
    data = gzip.compress((json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n").encode(), mtime=0)
    options.output.parent.mkdir(parents=True, exist_ok=True)
    options.output.write_bytes(data)
    print(json.dumps(dict(cases=len(cases), successful=sum(c["expected"]["status"] == "ok" for c in cases),
                          pillow=sum(c["pillow"] for c in cases), sha256=sha(data))))


if __name__ == "__main__":
    main()
