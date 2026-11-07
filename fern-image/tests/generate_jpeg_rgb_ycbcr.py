"""Create exact rational observations for forward JFIF sample conversion."""

import argparse
from fractions import Fraction
import fractions
import gzip
import hashlib
import itertools
import json
from pathlib import Path
import platform
import random
import sys


SPECIFICATION = {"url": "https://www.w3.org/Graphics/JPEG/jfif3.pdf", "page": 3,
                 "sha256": "1135325aa12ee6b3def5a374fecdf3d6d0de88dc4f205af7de8d25a9c949e173"}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def converted(r, g, b):
    values = [Fraction("0.299") * r + Fraction("0.587") * g + Fraction("0.114") * b,
              -Fraction("0.1687") * r - Fraction("0.3313") * g + Fraction("0.5") * b + 128,
              Fraction("0.5") * r - Fraction("0.4187") * g - Fraction("0.0813") * b + 128]
    return [max(0, min(255, (value + Fraction(1, 2)).numerator // (value + Fraction(1, 2)).denominator))
            for value in values]


def triples():
    values = dict.fromkeys((r, g, 128) for r in range(256) for g in range(256))
    grid = [0, 1, 16, 64, 127, 128, 129, 235, 254, 255]
    values.update(dict.fromkeys(itertools.product(grid, repeat=3)))
    for value in range(256):
        for triple in [(value, value, value), (value, 0, 0), (0, value, 0), (0, 0, value),
                       (value, value, 128), (value, 128, 128)]:
            values[triple] = None
    generator = random.Random(14475)
    for _ in range(2048):
        values[tuple(generator.randrange(256) for _ in range(3))] = None
    return list(values)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("bundle", type=Path)
    parser.add_argument("--small", action="store_true")
    args = parser.parse_args()
    paths = {"generator": Path(__file__).resolve(), "Python": Path(sys.executable).resolve(),
             "fractions": Path(fractions.__file__).resolve(), "random": Path(random.__file__).resolve()}
    identities = {name: sha(path.read_bytes()) for name, path in paths.items()}
    source = triples()
    if args.small:
        source = [(0, 0, 0), (255, 255, 255), (255, 0, 0), (0, 255, 0), (0, 0, 255),
                  (2, 130, 128), (1, 1, 128), (1, 128, 128)]
    cases = []
    for first in range(0, len(source), 512):
        batch = source[first:first + 512]
        encoded = bytes(value for triple in batch for value in triple)
        output = bytes(value for triple in batch for value in converted(*triple))
        cases.append({"name": f"triples-{first:06d}", "count": len(batch), "input_hex": encoded.hex(),
                      "ycbcr_hex": output.hex(), "input_sha256": sha(encoded), "ycbcr_sha256": sha(output)})
    if identities != {name: sha(path.read_bytes()) for name, path in paths.items()}:
        raise ValueError("reference source or tool changed")
    record = {"schema": 1, "reference": {"specification": SPECIFICATION, "Python": platform.python_version(),
              "sha256": identities, "method": "Exact Fraction evaluation of printed forward equations; nearest integer with ties upward, then clipping.",
              "scope": "Finite explicit RGB8 triples, not encoded JPEG, profiles, chroma filters, or codec parity."},
              "triples": len(source), "cases": cases}
    args.bundle.parent.mkdir(parents=True, exist_ok=True)
    args.bundle.write_bytes(gzip.compress(json.dumps(record, separators=(",", ":")).encode(), mtime=0))
    print(f"created {len(source)} triples in {len(cases)} rows")


if __name__ == "__main__":
    main()
