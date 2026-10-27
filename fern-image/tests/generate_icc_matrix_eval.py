"""Construct exact rational matrix observations independently of the Fern arithmetic."""
from fractions import Fraction
import gzip
import hashlib
import itertools
import json
from pathlib import Path
import platform
import random
import struct

ROOT = Path(__file__).resolve().parents[1]

def bits(value):
    return struct.unpack('<q', struct.pack('<d', value))[0]

def value(raw):
    return struct.unpack('<d', struct.pack('<q', raw))[0]

def packet(case):
    return b'FME1' + struct.pack('<iiqqq', case['cancel_at'], len(case['coefficients']), *case['input_bits']) + struct.pack('<' + 'i' * len(case['coefficients']), *case['coefficients'])

def expected(coefficients, inputs, cancel):
    if len(coefficients) != 9:
        return dict(status='error', error='Shape', polls=0)
    import math
    numbers = list(map(value, inputs))
    if any(not math.isfinite(x) or not 0 <= x <= 1 for x in numbers):
        return dict(status='error', error='Input', polls=0)
    if cancel in (1, 2):
        return dict(status='error', error='Cancelled', polls=cancel)
    fractions, exact = [], []
    for i in range(3):
        row = coefficients[3*i:3*i+3]
        dot = sum((Fraction(c, 65536) * Fraction(x) for c, x in zip(row, numbers)), Fraction())
        fractions.append([str(dot.numerator), str(dot.denominator)])
        projection = [j for j in range(3) if row[j] == 65536 and all(row[k] == 0 for k in range(3) if k != j)]
        exact.append(inputs[projection[0]] if projection else 0 if dot == 0 else bits(float(dot)) if Fraction(float(dot)) == dot else None)
    return dict(status='ok', polls=2, fractions=fractions, exact_bits=exact)

def generate():
    cases = []
    def add(name, coefficients, inputs, cancel=0):
        c = dict(name=name, coefficients=list(coefficients), input_bits=list(inputs), cancel_at=cancel)
        c['expected'] = expected(c['coefficients'], c['input_bits'], cancel)
        c['payload_sha256'] = hashlib.sha256(packet(c)).hexdigest()
        cases.append(c)
    edge = [0, bits(-0.0), 1, 2, 32768, 65535, 65536, 0x000fffffffffffff, 0x0010000000000000, 0x0010000000000001, bits(0.25), bits(0.5)-1, bits(0.5), bits(0.5)+1, bits(1.0)-1, bits(1.0)]
    for n, perm in enumerate(itertools.permutations(range(3))):
        matrix = [65536 if perm[i] == j else 0 for i in range(3) for j in range(3)]
        for j, v in enumerate(edge):
            add(f'projection-{n}-{j}', matrix, [v, edge[(j+3)%len(edge)], edge[(j+9)%len(edge)]])
    for n, row in enumerate(itertools.product([-2147483648, -65536, 0, 65536, 2147483647], repeat=3)):
        matrix = list(row) + list(row[1:] + row[:1]) + list(reversed(row))
        for j in [0, 1, 6, 7, 9, 11, 14, 15]:
            add(f'signed-extremes-{n}-{j}', matrix, [edge[j]]*3)
    rng = random.Random(13672)
    for n in range(384):
        row = [rng.randrange(-2**31, 2**31) for _ in range(9)]
        inputs = [rng.randrange(bits(1.0)+1) if n%2 else bits(rng.random()) for _ in range(3)]
        add(f'random-{n}', row, inputs)
    for n in range(128):
        a = rng.randrange(1, 2**31)
        x = edge[n%len(edge)] if n<32 else bits(rng.random())
        z = edge[(n*7)%len(edge)]
        for perm in [(0,1,2), (2,0,1), (1,2,0)]:
            row = [a, -a, rng.choice([-2147483648, -65536, -1, 1, 65536, 2147483647])]
            inputs = [x,x,z]
            add(f'cancellation-{n}-{perm[0]}', [row[j] for j in perm]*3, [inputs[j] for j in perm])
    profile = (ROOT/'tests/fixtures/icc-feasibility-srgb.icc').read_bytes()
    columns = [struct.unpack_from('>iii', profile, o+8) for o in (460,500,480)]
    matrix = [columns[j][i] for i in range(3) for j in range(3)]
    for n, inputs in enumerate(itertools.product([bits(0.0), bits(0.25), bits(0.5), bits(0.75), bits(1.0)],repeat=3)):
        add(f'frozen-profile-{n}', matrix, inputs)
    for n, count in enumerate([0,1,3,8,10]):
        add(f'shape-{n}', [0]*count, [bits(0.5)]*3)
    for j in range(3):
        for n,x in enumerate([bits(-0.1), bits(1.0000000000000002), bits(float('inf')), bits(float('-inf')), bits(float('nan')), -1]):
            inputs=[bits(0.5)]*3;inputs[j]=x
            add(f'input-{j}-{n}',matrix,inputs)
    for stop in (1,2,3):
        add(f'cancel-{stop}',matrix,[bits(0.25),bits(0.5),bits(0.75)],stop)
    assert len({c['name'] for c in cases}) == len(cases)
    return dict(schema=1,reference=dict(method='Exact Fraction dot products of signed fixed-point coefficients and the exact input IEEE values',criterion='At most two binary64 representable steps from correctly rounded exact values; exact projection bits and specified dyadic values',seed=13672,Python=platform.python_version(),sha256=dict(generator=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),profile=hashlib.sha256(profile).hexdigest())),cases=cases)

if __name__ == '__main__':
    out=ROOT/'tests/fixtures/icc-matrix-eval-reference.json.gz'
    out.write_bytes(gzip.compress((json.dumps(generate(),sort_keys=True,separators=(',',':'))+'\n').encode(),mtime=0))
    print(out,hashlib.sha256(out.read_bytes()).hexdigest())
