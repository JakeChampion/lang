"""Construct exact branch/affine references and independent 90-digit Decimal powers."""

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
    values = case["values"]
    return b"FPE1" + struct.pack("<iiiq", case["function_type"], case["cancel_at"], len(values), case["input_bits"]) + struct.pack("<" + str(len(values)) + "i", *values)


def reference(function, raw, x):
    v = [Fraction(n, 65536) for n in raw]
    identity = raw[0] == 65536 and (function == 0 or raw[1:3] == [65536, 0]
                and (function == 1 or function == 2 and raw[3] == 0 or function == 3 and raw[3] == 65536
                     or function == 4 and raw[3] == 65536 and raw[5:] == [0, 0]))
    if identity:
        f = Fraction(x)
        return dict(fraction=[str(f.numerator), str(f.denominator)], exact_bits=bits(x))
    t = Fraction(x)
    if function >= 3 and t < v[4]:
        f = v[3] * t + (v[6] if function == 4 else 0)
        f = max(Fraction(0), min(Fraction(1), f))
        return dict(fraction=[str(f.numerator), str(f.denominator)])
    base = t if function == 0 else max(Fraction(0), v[1] * t + v[2])
    offset = v[3] if function == 2 else v[5] if function == 4 else Fraction(0)
    if v[0] == 1:
        f = max(Fraction(0), min(Fraction(1), base + offset))
        return dict(fraction=[str(f.numerator), str(f.denominator)])
    with localcontext() as context:
        context.prec = 90
        context.Emin = -999999999
        context.Emax = 999999999
        arg = Decimal(base.numerator) / Decimal(base.denominator)
        gamma = Decimal(raw[0]) / 65536
        result = context.power(arg, gamma) + Decimal(offset.numerator) / Decimal(offset.denominator)
        return dict(decimal=str(max(Decimal(0), min(Decimal(1), result))))


def generate():
    cases = []
    rng = random.Random(13669)

    def add(name, function, values, x, error=None, stop=0):
        case = dict(name=name, function_type=function, values=list(values), input_bits=bits(x), cancel_at=stop)
        expected = dict(status="error", error=error, polls=stop if error == "Cancelled" else 0) if error else dict(status="ok", polls=2, **reference(function, list(values), x))
        case.update(expected=expected, payload_sha256=sha(packet(case)))
        cases.append(case)

    inputs = [0.0, -0.0, math.ulp(0.0), float.fromhex("0x1p-1022"), 0.125, 0.25, 1 / 3, 0.5, 0.75, math.nextafter(1, 0), 1.0]
    for gamma in [1, 2, 32768, 65535, 65536, 65537, 157286, 2147483647]:
        for i, x in enumerate(inputs):
            add(f"power-zero-{gamma}-{i}", 0, [gamma], x)
    for function, values in [(0, [65536]), (1, [65536, 65536, 0]), (2, [65536, 65536, 0, 0]),
                             (3, [65536, 65536, 0, 65536, 32768]), (4, [65536, 65536, 0, 65536, 32768, 0, 0])]:
        for i, x in enumerate(inputs):
            add(f"identity-{function}-{i}", function, values, x)
    for function in range(1, 5):
        for gamma in [65536, 65537, 157286, 2147483647]:
            for a, b in [(65536, 0), (65536, -16384), (49152, -16384), (2147450880, -2147385344)]:
                if function <= 2:
                    values = [gamma, a, b] + ([16384] if function == 2 else [])
                    boundary = -b / a
                else:
                    d = 65536 if a > 65536 else 32768
                    values = [gamma, a, b, 5072, d] + ([-16384, 8192] if function == 4 else [])
                    boundary = d / 65536
                points = [*inputs, math.nextafter(boundary, 0), boundary, math.nextafter(boundary, 1)]
                for i, x in enumerate(points):
                    if 0 <= x <= 1:
                        add(f"branches-{function}-{gamma}-{a}-{b}-{i}", function, values, x)
    for i in range(160):
        function = rng.randrange(1, 5)
        gamma = rng.randrange(65536, 2147483648)
        a = rng.randrange(1, 65537)
        b = rng.randrange(65537 - a)
        values = [gamma, a, b]
        if function == 2:
            values += [rng.randrange(-65536, 65537)]
        if function >= 3:
            values += [rng.randrange(-65536, 65537), rng.randrange(65537)]
        if function == 4:
            values += [rng.randrange(-65536, 65537), rng.randrange(-65536, 65537)]
        add(f"random-{i}", function, values, rng.random())
    for function in [2, 3, 4]:
        for edge in [-2147483648, -65536, -1, 0, 1, 65536, 2147483647]:
            values = [131072, 65536, 0, edge] if function == 2 else [131072, 65536, 0, edge, 32768] + ([edge, -edge if edge != -2147483648 else 2147483647] if function == 4 else [])
            for i, x in enumerate([0.0, 0.25, 0.5, 1.0]):
                add(f"clipping-{function}-{edge}-{i}", function, values, x)

    frozen = Path(__file__).with_name("fixtures") / "icc-feasibility-srgb.icc"
    manifest = json.loads(frozen.with_name("icc-feasibility-reference.json").read_text())
    raw = frozen.read_bytes()
    assert sha(raw) == manifest["sha256"]
    for tag in manifest["tags"]:
        if tag["signature"] in ["rTRC", "gTRC", "bTRC"]:
            values = list(struct.unpack_from(">5i", raw, tag["offset"] + 12))
            for i, x in enumerate(inputs + [math.nextafter(values[4] / 65536, 0), values[4] / 65536, math.nextafter(values[4] / 65536, 1)]):
                add(f"frozen-{tag['signature']}-{i}", 3, values, x)
    valid_shapes = [[65536], [65536, 65536, 0], [65536, 65536, 0, 0], [65536, 65536, 0, 65536, 32768], [65536, 65536, 0, 65536, 32768, 0, 0]]
    for function, values in enumerate(valid_shapes):
        for stop in [1, 2]:
            add(f"cancel-{function}-{stop}", function, values, 0.375, "Cancelled", stop)
        for count in range(9):
            if count != len(values):
                add(f"shape-{function}-{count}", function, [65536] * count, 0.5, "Shape")
        for gamma in [-2147483648, -1, 0]:
            add(f"gamma-domain-{function}-{gamma}", function, [gamma, *values[1:]], 0.5, "Domain")
        for i, x in enumerate([-0.001, math.nextafter(1, math.inf), -math.inf, math.inf, math.nan]):
            add(f"input-{function}-{i}", function, values, x, "Input")
        if function:
            for a in [-2147483648, -1, 0]:
                bad = list(values)
                bad[1] = a
                add(f"a-domain-{function}-{a}", function, bad, 0.5, "Domain")
            for gamma in [1, 32768, 65535]:
                add(f"fractional-affine-{function}-{gamma}", function, [gamma, *values[1:]], 0.5, "Unsupported")
            bad = list(values)
            bad[2] = 1
            add(f"over-white-{function}", function, bad, 0.5, "Unsupported")
        if function >= 3:
            for d in [-1, 65537]:
                bad = list(values)
                bad[4] = d
                add(f"d-subset-{function}-{d}", function, bad, 0.5, "Unsupported")
            bad = list(values)
            bad[2] = -65536
            add(f"active-base-domain-{function}", function, bad, 0.5, "Domain")
    for function in [-2147483648, -1, 5, 2147483647]:
        add(f"function-{function}", function, [65536], 0.5, "Function")
    assert len({c["name"] for c in cases}) == len(cases)
    return dict(schema=1, reference=dict(sha256=dict(generator=sha(Path(__file__).read_bytes()), frozen_profile=sha(raw)),
                criterion="Absolute error <= 1e-12 for this finite declared subset; exact IEEE identities. Not a universal error bound.",
                construction="Exact Fraction branch and affine expressions from original IEEE bits and signed wire values; 90-digit Decimal power and standard clipping.",
                decimal_precision=90, decimal_exponent_range=[-999999999, 999999999], seed=13669), cases=cases)


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
