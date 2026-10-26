"""Build finite inverse-DCT observations from T.81 math and offline grayscale JPEGs."""

import argparse
import decimal
import gzip
import hashlib
import json
from pathlib import Path
import platform
import random
import struct
import subprocess
import sys
import tempfile

PI = decimal.Decimal("3.1415926535897932384626433832795028841971693993751058209749445923078164062862089986280348253421170679")


def sha(data):
    return hashlib.sha256(data).hexdigest()


def zigzag():
    order = []
    for diagonal in range(15):
        pairs = [(row, diagonal - row) for row in range(8) if 0 <= diagonal - row < 8]
        if diagonal % 2 == 0:
            pairs.reverse()
        order.extend(row * 8 + column for row, column in pairs)
    assert sorted(order) == list(range(64))
    return order


def cosine(angle):
    while angle > PI:
        angle -= 2 * PI
    term = decimal.Decimal(1)
    total = term
    for n in range(1, 300):
        term = -term * angle * angle / decimal.Decimal((2 * n - 1) * (2 * n))
        total += term
        if abs(term) < decimal.Decimal("1e-88"):
            return total
    raise ValueError("reference cosine did not converge")


def basis():
    return [[(decimal.Decimal(1) / decimal.Decimal(2).sqrt() if u == 0 else decimal.Decimal(1))
             * cosine(decimal.Decimal((2 * x + 1) * u) * PI / 16) / 2
             for u in range(8)] for x in range(8)]


def ideal(coefficients, quantizers, matrix):
    natural = [0] * 64
    for index, n in enumerate(zigzag()):
        natural[n] = quantizers[index]
    dequantized = [c * q for c, q in zip(coefficients, natural, strict=True)]
    output = []
    for y in range(8):
        for x in range(8):
            # Direct two-dimensional sum, with 90-digit arithmetic.
            value = sum((decimal.Decimal(dequantized[v * 8 + u]) * matrix[x][u] * matrix[y][v]
                         for v in range(8) for u in range(8)), decimal.Decimal(0))
            output.append(value)
    return output


def pixels(values):
    return [min(255, max(0, int((value + decimal.Decimal("128.5")).to_integral_value(rounding=decimal.ROUND_FLOOR))))
            for value in values]


def mathematical_cases():
    yield "zero", [0] * 64, [1] * 64
    for dc in [-32768, -2048, -1024, -1016, -12, -8, -4, -1, 1, 4, 8, 12, 1016, 1024, 2047, 32767]:
        for q in [1, 255]:
            yield f"dc-{dc}-q{q}", [dc] + [0] * 63, [q] * 64
    for index in range(64):
        for value in [-1023, -1, 1, 1023]:
            coefficients = [0] * 64
            coefficients[index] = value
            yield f"basis-{index}-{value}", coefficients, [1 + (i * 17) % 255 for i in range(64)]
    yield "domain-all-max", [32767] * 64, [255] * 64
    yield "domain-all-min", [-32768] * 64, [255] * 64
    yield "domain-alternating", [32767 if i % 2 == 0 else -32768 for i in range(64)], [1 if i % 2 == 0 else 255 for i in range(64)]
    yield "domain-checkerboard", [32767 if (i // 8 + i % 8) % 2 == 0 else -32768 for i in range(64)], [255] * 64
    rng = random.Random(20261026)
    for i in range(64):
        span = 32768 if i % 4 == 0 else 1024
        yield f"dense-{i}", [rng.randrange(-span, span) for _ in range(64)], [rng.randrange(1, 256) for _ in range(64)]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=Path)
    parser.add_argument("--oracle", type=Path, required=True)
    parser.add_argument("--jpeg-reference", type=Path, default=Path(__file__).parent / "fixtures/jpeg-coefficients-reference.json.gz")
    parser.add_argument("--bundle", type=Path)
    parser.add_argument("--basis", type=Path)
    args = parser.parse_args()
    args.directory.mkdir(parents=True, exist_ok=True)
    tool_paths = {"generator": Path(__file__).resolve(), "python": Path(sys.executable).resolve(),
                  "oracle": args.oracle.resolve(), "oracle_source": Path(__file__).with_name("reference_jpeg_idct.c"),
                  "coefficient_bundle": args.jpeg_reference.resolve(), "random": Path(random.__file__).resolve(),
                  "decimal": Path(decimal.__file__).resolve()}
    import _decimal
    if getattr(_decimal, "__file__", None):
        tool_paths["decimal_extension"] = Path(_decimal.__file__).resolve()
    hashes = {name: sha(path.read_bytes()) for name, path in tool_paths.items()}
    cases = []
    with decimal.localcontext() as context:
        context.prec = 90
        matrix = basis()
        if args.basis:
            args.basis.write_text("\n".join("  " + ", ".join(repr(float(v)) for v in row) + "," for row in matrix) + "\n")

        def add(name, coefficients, quantizers, codec=None, origin=None):
            if len(coefficients) != 64 or len(quantizers) != 64 or not all(-32768 <= c <= 32767 for c in coefficients) or not all(1 <= q <= 255 for q in quantizers):
                raise ValueError("reference input outside kernel domain")
            encoded = struct.pack("<64h64B", *coefficients, *quantizers)
            values = ideal(coefficients, quantizers, matrix)
            entry = {"name": name, "encoded_hex": encoded.hex(), "input_sha256": sha(encoded),
                     "samples_decimal": [str(v) for v in values], "ideal_pixels": pixels(values)}
            if codec is not None:
                entry["codec_pixels"] = codec
                entry["jpeg_origin"] = origin
            cases.append(entry)
            (args.directory / (name + ".bin")).write_bytes(encoded)

        for name, coefficients, quantizers in mathematical_cases():
            add(name, coefficients, quantizers)
        coefficient_bundle = json.loads(gzip.decompress(args.jpeg_reference.read_bytes()))
        observations = []
        with tempfile.TemporaryDirectory(prefix="jpeg-idct-oracle-") as temporary:
            jpeg = Path(temporary) / "reference.jpg"
            for case in coefficient_bundle["cases"]:
                components = case["reference"]["components"]
                if len(components) != 1:
                    continue
                encoded = bytes.fromhex(case["encoded_hex"])
                jpeg.write_bytes(encoded)
                result = subprocess.run([str(args.oracle.resolve()), str(jpeg)], capture_output=True, check=True, timeout=20)
                if result.stderr:
                    raise ValueError("offline decoder reported a warning")
                observation = json.loads(result.stdout)
                width, height = observation["width"], observation["height"]
                if width != case["reference"]["width"] or height != case["reference"]["height"] or len(observation["pixels"]) != width * height:
                    raise ValueError("offline decoder dimensions disagree")
                origin = {"name": case["name"], "input_sha256": sha(encoded), "oracle_stdout_sha256": sha(result.stdout)}
                observations.append(origin)
                component = components[0]
                natural = observation["quantizers_natural"]
                quantizers = [natural[n] for n in zigzag()]
                for y in range(component["rows"]):
                    for x in range(component["columns"]):
                        codec = [observation["pixels"][(y * 8 + vy) * width + x * 8 + vx]
                                 if y * 8 + vy < height and x * 8 + vx < width else None
                                 for vy in range(8) for vx in range(8)]
                        add(f"jpeg-{case['name']}-{x}-{y}", component["blocks"][y * component["columns"] + x], quantizers, codec, {**origin, "block_x": x, "block_y": y})
    if hashes != {name: sha(path.read_bytes()) for name, path in tool_paths.items()}:
        raise ValueError("reference source or tool changed")
    document = {"schema": 1, "reference": {"math": "T.81 A.3 direct 2D sum, Decimal precision 90",
                "rounding": "inverse level shift, nearest integer with ties toward positive infinity, clamp 0..255",
                "codec": "libjpeg-turbo 2.1.5 JDCT_ISLOW grayscale observations", "Python": platform.python_version(),
                "sha256": hashes, "jpeg_observations": observations,
                "scope": "Finite scalar component-block observations. No complete JPEG pixel decoder or standards precision certification."},
                "cases": cases}
    (args.directory / "reference.json").write_text(json.dumps(document, indent=2) + "\n")
    if args.bundle:
        args.bundle.parent.mkdir(parents=True, exist_ok=True)
        args.bundle.write_bytes(gzip.compress(json.dumps(document, separators=(",", ":")).encode(), mtime=0))
    print(f"created {len(cases)} blocks, {len(observations)} independent grayscale JPEG observations")


if __name__ == "__main__":
    main()
