"""Construct finite IEEE inputs with independent Fraction and 90-digit Decimal references."""

import argparse
from decimal import Decimal, localcontext
from fractions import Fraction
import gzip
import hashlib
import json
import math
from pathlib import Path
import random
import struct


def sha(data):
    return hashlib.sha256(data).hexdigest()


def bits(value):
    return struct.unpack("<q", struct.pack("<d", value))[0]


def packet(case):
    return b"FCE1" + struct.pack("<iiiiiq", case["kind"], case["max_samples"], case["cancel_at"],
                                  case["parameter"], len(case["values"]), case["input_bits"]) + struct.pack("<" + str(len(case["values"])) + "i", *case["values"])


def generate():
    cases = []
    rng = random.Random(13665)

    def add(name, kind, x, values=(), parameter=0, maximum=4096, error=None, polls=None, stop=0):
        values = list(values)
        count = 2 if kind in (0, 1) else 2 + (len(values) + 63) // 64
        case = dict(name=name, kind=kind, input_bits=bits(x), values=values, parameter=parameter,
                    max_samples=maximum, cancel_at=stop)
        if error:
            expected = dict(status="error", error=error, polls=stop if stop else polls or 0)
        else:
            expected = dict(status="ok", polls=count)
            if kind == 0 or kind == 1 and (parameter == 256 or x in (0, 1)):
                expected["exact_bits"] = bits(x)
                rational = Fraction(x)
            elif kind == 1:
                with localcontext() as context:
                    context.prec = 90
                    expected["decimal"] = str(context.power(Decimal.from_float(x), Decimal(parameter) / 256))
                rational = None
            else:
                position = Fraction(x) * (len(values) - 1)
                index = min(position.numerator // position.denominator, len(values) - 2)
                rational = (values[index] + (values[index + 1] - values[index]) * (position - index)) / 65535
            if rational is not None:
                expected["fraction"] = [str(rational.numerator), str(rational.denominator)]
        case["expected"] = expected
        case["payload_sha256"] = sha(packet(case))
        cases.append(case)

    inputs = [0.0, -0.0, math.ulp(0.0), math.nextafter(float.fromhex("0x1p-1022"), 0), float.fromhex("0x1p-1022"),
              0.125, 0.25, 1 / 3, 0.5, 0.75, math.nextafter(1.0, 0.0), 1.0]
    for i, x in enumerate(inputs):
        add(f"identity-{i}", 0, x)
    for gamma in [1, 2, 3, 127, 128, 255, 256, 257, 563, 1024, 4096, 32767, 65535]:
        for i, x in enumerate(inputs):
            add(f"gamma-{gamma}-{i}", 1, x, parameter=gamma)
    for i in range(120):
        add(f"gamma-random-{i}", 1, rng.random(), parameter=rng.randrange(1, 65536))
    for n in [2, 3, 4, 63, 64, 65, 127, 128, 129, 4096, 65536]:
        tables = [[0 if i % 2 == 0 else 65535 for i in range(n)], [i * 65535 // (n - 1) for i in range(n)],
                  [rng.randrange(65536) for _ in range(n)]]
        for style, values in enumerate(tables):
            selected = inputs if n < 4096 else [0.0, 1 / 3, 0.5, 1.0]
            for i, x in enumerate(selected):
                add(f"samples-{n}-{style}-{i}", 2, x, values, maximum=max(n, 4096))
            for k in sorted({1, (n - 1) // 2, n - 2}):
                center = k / (n - 1)
                for side, x in enumerate([math.nextafter(center, 0), center, math.nextafter(center, 1)]):
                    add(f"knot-{n}-{style}-{k}-{side}", 2, x, values, maximum=max(n, 4096))
    for i in range(80):
        values = [rng.randrange(65536) for _ in range(rng.randrange(2, 257))]
        add(f"samples-random-{i}", 2, rng.random(), values)
    for i, x in enumerate([-math.inf, math.inf, math.nan, -0.001, math.nextafter(1, math.inf)]):
        for kind in range(4):
            add(f"input-error-{i}-{kind}", kind, x, [0, 65535], parameter=256, error="Input")
    for gamma in [-2147483648, -1, 0, 65536, 2147483647]:
        add(f"gamma-error-{gamma}", 1, 0.5, parameter=gamma, error="Value")
    for maximum in [-2147483648, -1, 0]:
        for kind in range(4):
            add(f"limit-error-{maximum}-{kind}", kind, 0.5, error="Limit", maximum=maximum)
    for values in [[], [1]]:
        add(f"shape-{len(values)}", 2, 0.5, values, error="Shape")
    for i, values in enumerate([[-1, 65535], [0, 65536], [0, 65535, -1]]):
        add(f"sample-value-{i}", 2, 0.0, values, error="Value", polls=2)
    for n, maximum in [(3, 2), (4097, 4096), (65537, 2147483647)]:
        add(f"sample-limit-{n}", 2, 0.5, [0] * n, maximum=maximum, error="Limit")
    for function in range(5):
        add(f"parametric-unsupported-{function}", 3, 0.5, [65536], parameter=function, error="Unsupported")
    for n in [2, 63, 64, 65, 130]:
        for stop in range(1, 3 + (n + 63) // 64):
            add(f"cancel-samples-{n}-{stop}", 2, 0.375, list(range(n)), error="Cancelled", stop=stop)
    for kind in [0, 1]:
        for stop in [1, 2]:
            add(f"cancel-{kind}-{stop}", kind, 0.5, parameter=563, error="Cancelled", stop=stop)
    assert len({c["name"] for c in cases}) == len(cases)
    return dict(schema=1, reference=dict(sha256=dict(generator=sha(Path(__file__).read_bytes())),
                criterion="Absolute error <= 1e-12 for this finite corpus, exact IEEE bits for identities and gamma endpoints; not a universal error bound.",
                construction="Exact Fraction interpolation of the original IEEE binary64 input and raw u16 values; independent Decimal power at 90-digit precision.",
                seed=13665, decimal_precision=90), cases=cases)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("output", type=Path)
    options = parser.parse_args()
    document = generate()
    options.output.parent.mkdir(parents=True, exist_ok=True)
    options.output.write_bytes(gzip.compress(json.dumps(document, sort_keys=True, separators=(",", ":")).encode(), mtime=0))
    print(json.dumps(dict(cases=len(document["cases"]), sha256=sha(options.output.read_bytes()))))


if __name__ == "__main__":
    main()
