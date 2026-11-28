"""Construct sRGB destination bytes with exact arithmetic, plus offline LittleCMS observations."""
from decimal import Decimal, localcontext
from fractions import Fraction
import gzip
import hashlib
import itertools
import json
import math
from pathlib import Path
import platform
import random
import struct
import sys

ROOT = Path(__file__).resolve().parents[1]
MATRIX = [28576, 25239, 9375, 14581, 46983, 3972, 912, 6361, 46787]
BOUNDARY = Fraction(1, 10**9)
THRESHOLD, SLOPE = Fraction("0.0031308"), Fraction("12.92")
SCALE, OFFSET = Fraction("1.055"), Fraction("0.055")


def bits(value):
    return struct.unpack('<q', struct.pack('<d', value))[0]


def value(raw):
    return struct.unpack('<d', struct.pack('<q', raw))[0]


def packet(case):
    return b'FSD1' + struct.pack('<iqqq', case['cancel_at'], *case['input_bits'])


def solve(numbers):
    rows = [[Fraction(MATRIX[3*i+j], 65536) for j in range(3)] + [Fraction(numbers[i])] for i in range(3)]
    for column in range(3):
        pivot = next(i for i in range(column, 3) if rows[i][column])
        rows[column], rows[pivot] = rows[pivot], rows[column]
        divisor = rows[column][column]
        rows[column] = [v/divisor for v in rows[column]]
        for i in range(3):
            if i != column:
                multiplier = rows[i][column]
                rows[i] = [a-multiplier*b for a, b in zip(rows[i], rows[column], strict=True)]
    return [row[3] for row in rows]


def encode(linear):
    """Return 255 times the IEC 61966-2-1 encoding of the clipped value, to 90 digits."""
    linear = min(max(linear, Fraction(0)), Fraction(1))
    with localcontext() as context:
        context.prec = 90
        if linear <= THRESHOLD:
            scaled = linear * SLOPE * 255
            return Decimal(scaled.numerator) / Decimal(scaled.denominator)
        x = Decimal(linear.numerator) / Decimal(linear.denominator)
        power = (x.ln() / Decimal("2.4")).exp()
        return (Decimal("1.055") * power - Decimal("0.055")) * 255


def quantize(scaled):
    with localcontext() as context:
        context.prec = 90
        byte = int((scaled + Decimal("0.5")).to_integral_value(rounding="ROUND_FLOOR"))
        nearest = (scaled - Decimal("0.5")).to_integral_value(rounding="ROUND_HALF_EVEN") + Decimal("0.5")
        near = abs(scaled - nearest) < Decimal(1) / Decimal(10**9) and 0 <= nearest <= 255
        allowed = sorted({byte, int(nearest - Decimal("0.5")), int(nearest + Decimal("0.5"))} & set(range(256))) if near else [byte]
        return byte, allowed, float(abs(scaled - nearest))


def expected(inputs, cancel):
    numbers = list(map(value, inputs))
    if any(not math.isfinite(x) for x in numbers):
        return dict(status='error', error='Input', polls=0)
    if any(abs(x) > 2 for x in numbers):
        return dict(status='error', error='Unsupported', polls=0)
    if cancel in (1, 2):
        return dict(status='error', error='Cancelled', polls=cancel)
    bytes_, allowed, gaps = [], [], []
    for linear in solve(numbers):
        byte, permitted, gap = quantize(encode(linear))
        bytes_.append(byte); allowed.append(permitted); gaps.append(gap)
    return dict(status='ok', polls=2, bytes=bytes_, allowed=allowed, boundary_gaps=gaps)


def column_sum(t, mask):
    return [t * sum(MATRIX[3*i+j] for j in range(3) if mask[j]) / 65536 for i in range(3)]


def forward(linear):
    return [sum(Fraction(MATRIX[3*i+j], 65536) * linear[j] for j in range(3)) for i in range(3)]


def inverse_encode(e):
    if e <= THRESHOLD * SLOPE:
        return e / SLOPE
    with localcontext() as context:
        context.prec = 60
        base = (Decimal(e.numerator) / Decimal(e.denominator) + Decimal("0.055")) / Decimal("1.055")
        return Fraction((base.ln() * Decimal("2.4")).exp())


def generate(lcms=None):
    cases = []
    def add(name, numbers, cancel=0, control=False, lcms_applicable=None):
        raw = [bits(float(x)) if not isinstance(x, int) else x for x in numbers]
        c = dict(name=name, input_bits=raw, cancel_at=cancel, control=control)
        c['expected'] = expected(raw, cancel)
        if control:
            assert c['expected']['status'] == 'ok' and all(len(a) == 1 for a in c['expected']['allowed']), name
        nonnegative = all(value(r) >= 0 for r in raw)
        c['lcms_applicable'] = c['expected']['status'] == 'ok' and nonnegative if lcms_applicable is None else lcms_applicable
        c['payload_sha256'] = hashlib.sha256(packet(c)).hexdigest()
        cases.append(c)
    masks = [m for m in itertools.product([False, True], repeat=3) if any(m)]
    add('control-black', [0.0, 0.0, 0.0], control=True)
    add('control-negative-zero', [-0.0, -0.0, -0.0], control=True)
    for k in range(0, 4097, 16):
        for n, mask in enumerate(masks):
            add(f'control-columns-{n}-{k}', column_sum(k / 4096, mask), control=True)
    for k in range(4097, 8193, 256):
        for n, mask in enumerate(masks):
            add(f'clip-columns-{n}-{k}', column_sum(k / 4096, mask))
    for n, inputs in enumerate(itertools.product([-2.0, -0.5, -0.0, 0.0, 0.25, 0.75, 1.0, 2.0], repeat=3)):
        add(f'grid-{n}', inputs)
    rng = random.Random(13659)
    for n in range(1024):
        add(f'random-gamut-{n}', [rng.uniform(0, 1) * w for w in (0.9642, 1.0, 0.8249)])
    for n in range(512):
        add(f'random-domain-{n}', [rng.uniform(-2, 2) for _ in range(3)])
    for n in range(256):
        boundary = [inverse_encode((Fraction(rng.randrange(255)) + Fraction(1, 2)) / 255) for _ in range(3)]
        add(f'byte-boundary-{n}', [float(x) for x in forward(boundary)])
    for n in range(3):
        for k, v in enumerate([bits(float('inf')), bits(float('-inf')), bits(float('nan')), bits(2.0) + 1, bits(-2.0) + 1, bits(float.fromhex('0x1.fffffffffffffp1023'))]):
            raw = [bits(0.5)] * 3; raw[n] = v
            add(f'input-{n}-{k}', raw, cancel=1)
    for stop in (0, 1, 2, 3):
        add(f'cancel-{stop}', [0.25, 0.5, 0.75], cancel=stop)
    assert len({c['name'] for c in cases}) == len(cases)
    profile = (ROOT / 'tests/fixtures/icc-feasibility-srgb.icc').read_bytes()
    reference = dict(method='Exact Fraction Gaussian solve of the frozen sRGB colorants, Fraction clip, 90-digit Decimal IEC 61966-2-1 encoding, floor(255e + 1/2)',
                     criterion='Equal bytes; a difference of one only within 1e-9 of a byte boundary on the 255e scale; exact control bytes; LittleCMS within one code value',
                     seed=13659, Python=platform.python_version(),
                     sha256=dict(generator=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), profile=hashlib.sha256(profile).hexdigest()))
    if lcms is not None:
        applicable = [c for c in cases if c['lcms_applicable']]
        observations, provenance = lcms(profile, [[value(r) for r in c['input_bits']] for c in applicable])
        for case, observed in zip(applicable, observations, strict=True):
            case['lcms_bytes'] = observed
        reference['offline_cmm'] = provenance
    return dict(schema=1, reference=reference, cases=cases)


if __name__ == '__main__':
    sys.path.insert(0, str(Path(__file__).parent))
    from reference_icc_srgb_destination import observe
    out = ROOT / 'tests/fixtures/icc-srgb-destination-reference.json.gz'
    out.write_bytes(gzip.compress((json.dumps(generate(observe), sort_keys=True, separators=(',', ':')) + '\n').encode(), mtime=0))
    print(out, hashlib.sha256(out.read_bytes()).hexdigest())
