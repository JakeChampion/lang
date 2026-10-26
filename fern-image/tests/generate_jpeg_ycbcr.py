"""Generate finite exact rational observations for the explicit JFIF equations."""

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


def converted(y, cb, cr):
    samples = [Fraction(y) + Fraction("1.402") * (cr - 128),
               Fraction(y) - Fraction("0.34414") * (cb - 128) - Fraction("0.71414") * (cr - 128),
               Fraction(y) + Fraction("1.772") * (cb - 128)]
    return [max(0, min(255, (value + Fraction(1, 2)).numerator // (value + Fraction(1, 2)).denominator))
            for value in samples]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("bundle", type=Path)
    args = parser.parse_args()
    paths = {"generator": Path(__file__).resolve(), "Python": Path(sys.executable).resolve(),
             "fractions": Path(fractions.__file__).resolve(), "random": Path(random.__file__).resolve()}
    identities = {name: sha(path.read_bytes()) for name, path in paths.items()}
    values = dict.fromkeys((128, cb, cr) for cb in range(256) for cr in range(256))
    grid = [0, 1, 16, 32, 64, 78, 127, 128, 129, 178, 235, 254, 255]
    for triple in itertools.product(grid, repeat=3):
        values[triple] = None
    for y in range(256):
        for cb, cr in [(128, 128), (3, 128), (253, 128), (78, 178), (178, 78)]:
            values[(y, cb, cr)] = None
    generator = random.Random(13619)
    for _ in range(2048):
        values[tuple(generator.randrange(256) for _ in range(3))] = None
    triples = list(values)
    cases = []
    for first in range(0, len(triples), 512):
        batch = triples[first:first + 512]
        encoded = bytes(value for triple in batch for value in triple)
        rgb = bytes(value for triple in batch for value in converted(*triple))
        cases.append({"name": f"triples-{first:06d}", "count": len(batch), "input_hex": encoded.hex(),
                      "rgb_hex": rgb.hex(), "input_sha256": sha(encoded), "rgb_sha256": sha(rgb)})
    if identities != {name: sha(path.read_bytes()) for name, path in paths.items()}:
        raise ValueError("reference source or tool changed")
    record = {"schema": 1, "reference": {"specification": SPECIFICATION, "Python": platform.python_version(),
              "sha256": identities, "method": "Exact Fraction evaluation of the printed decimal equations; nearest integer with ties upward, then clipping.",
              "scope": "Finite explicit sample triples, not encoded JPEG, chroma reconstruction, or codec parity."},
              "triples": len(triples), "cases": cases}
    args.bundle.parent.mkdir(parents=True, exist_ok=True)
    args.bundle.write_bytes(gzip.compress(json.dumps(record, separators=(",", ":")).encode(), mtime=0))
    print(f"created {len(triples)} triples in {len(cases)} rows")


if __name__ == "__main__":
    main()
