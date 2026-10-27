"""Construct inverse observations with exact Gaussian elimination, independently of cofactors."""
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

ROOT = Path(__file__).resolve().parents[1]
BOUND = 98304

def bits(value):
    return struct.unpack('<q', struct.pack('<d', value))[0]

def value(raw):
    return struct.unpack('<d', struct.pack('<q', raw))[0]

def packet(case):
    return b'FMS1' + struct.pack('<iiqqq', case['cancel_at'], len(case['coefficients']), *case['input_bits']) + struct.pack('<'+'i'*len(case['coefficients']), *case['coefficients'])

def gaussian(coefficients, numbers):
    rows = [[Fraction(coefficients[3*i+j], 65536) for j in range(3)] + [Fraction(numbers[i])] for i in range(3)]
    for column in range(3):
        pivot = next((i for i in range(column, 3) if rows[i][column]), None)
        if pivot is None:
            return None
        rows[column], rows[pivot] = rows[pivot], rows[column]
        divisor = rows[column][column]
        rows[column] = [v/divisor for v in rows[column]]
        for i in range(3):
            if i != column:
                multiplier = rows[i][column]
                rows[i] = [a-multiplier*b for a,b in zip(rows[i], rows[column], strict=True)]
    return [row[3] for row in rows]

def expected(coefficients, inputs, cancel, exact=False):
    def error(name, polls=0): return dict(status='error', error=name, polls=polls)
    if len(coefficients) != 9: return error('Shape')
    numbers = list(map(value, inputs))
    if any(not math.isfinite(x) for x in numbers): return error('Input')
    if any(abs(c)>BOUND for c in coefficients) or any(abs(x)>2 for x in numbers): return error('Unsupported')
    solution = gaussian(coefficients, numbers)
    if solution is None: return error('Singular')
    if cancel in (1,2): return error('Cancelled', cancel)
    exact_bits=[]
    for j, result in enumerate(solution):
        projection = next((i for i in range(3) if abs(coefficients[3*i+j])==65536 and all(coefficients[3*i+k]==0 for k in range(3) if k!=j)), None)
        if projection is not None:
            exact_bits.append(bits(numbers[projection] if coefficients[3*projection+j]>0 else -numbers[projection]))
        elif result==0:
            exact_bits.append(0)
        elif exact:
            assert Fraction(float(result)) == result, 'declared dyadic result must be representable'
            exact_bits.append(bits(float(result)))
        else:
            exact_bits.append(None)
    return dict(status='ok', polls=2, fractions=[[str(v.numerator),str(v.denominator)]for v in solution], exact_bits=exact_bits)

def generate():
    cases=[]
    def add(name, coefficients, inputs, cancel=0, exact=False):
        c=dict(name=name,coefficients=list(coefficients),input_bits=list(inputs),cancel_at=cancel)
        c['expected']=expected(c['coefficients'],c['input_bits'],cancel,exact)
        c['payload_sha256']=hashlib.sha256(packet(c)).hexdigest()
        cases.append(c)
    unit=[65536,0,0,0,65536,0,0,0,65536]
    edge=[bits(x)for x in [-2.0,-1.0,-0.0,0.0,0.25,0.5,1.0,2.0]]+[1,-9223372036854775807,2,0x000fffffffffffff,0x0010000000000000,0x0010000000000001,bits(2.0)-1,bits(-2.0)-1]
    for n, perm in enumerate(itertools.permutations(range(3))):
        for signs in itertools.product([-1,1],repeat=3):
            matrix=[signs[i]*65536 if perm[i]==j else 0 for i in range(3)for j in range(3)]
            for k in range(len(edge)):
                add(f'projection-{n}-{signs}-{k}',matrix,[edge[k],edge[(k+3)%len(edge)],edge[(k+9)%len(edge)]])
    for scale in [1,2,16384,32768,65536,-1,-32768,-65536]:
        for k in range(8):
            add(f'dyadic-{scale}-{k}',[scale,0,0,0,scale,0,0,0,scale],[edge[k],edge[(k+3)%8],edge[(k+5)%8]],exact=True)
    for n in [2,3,17,255,32768,65535,98304]:
        matrix=[n,n-1,0,n-1,n-2,0,0,0,1]
        for k in range(len(edge)):
            for perm in [(0,1,2),(2,0,1),(1,2,0)]:
                transformed=[matrix[3*perm[i]+perm[j]]for i in range(3)for j in range(3)]
                add(f'unit-determinant-{n}-{k}-{perm}',transformed,[edge[k],edge[(k+7)%len(edge)],edge[(k+11)%len(edge)]])
    rng=random.Random(13694)
    for n in range(512):
        matrix=[rng.randint(-BOUND,BOUND)for _ in range(9)]
        inputs=[bits(rng.uniform(-2,2))for _ in range(3)]if n%2 else[bits(rng.choice([-1,1])*value(rng.randrange(bits(2.0)+1)))for _ in range(3)]
        add(f'random-{n}',matrix,inputs)
    for n in range(128):
        row=[rng.randint(-BOUND+1,BOUND-1)for _ in range(3)]
        matrix=row+[row[0]+1,row[1],row[2]]+[rng.randint(-BOUND,BOUND)for _ in range(3)]
        add(f'near-dependent-{n}',matrix,[bits(rng.uniform(-2,2))for _ in range(3)])
    for n,row in enumerate(itertools.product([-BOUND,-1,0,1,BOUND],repeat=3)):
        add(f'coefficient-edges-{n}',list(row)+[row[1],row[2],row[0]]+list(reversed(row)),[bits(-2.0),bits(0.5),bits(2.0)])
    profile=(ROOT/'tests/fixtures/icc-feasibility-srgb.icc').read_bytes()
    columns=[struct.unpack_from('>iii',profile,o+8)for o in (460,500,480)]
    matrix=[columns[j][i]for i in range(3)for j in range(3)]
    for n,inputs in enumerate(itertools.product([-2.0,-0.0,0.25,0.5,0.75,2.0],repeat=3)):
        add(f'frozen-profile-{n}',matrix,list(map(bits,inputs)))
    add('mandatory-naive-cancellation',matrix,[0x3fd806fad56a71b2,bits(0.5),bits(0.75)])
    for n,count in enumerate([0,1,3,8,10]):add(f'shape-{n}',[0]*count,[bits(0.5)]*3,cancel=1)
    for i in range(9):
        for v in [-2**31,-BOUND-1,BOUND+1,2**31-1]:
            m=unit.copy();m[i]=v;add(f'coefficient-range-{i}-{v}',m,[bits(0.5)]*3,cancel=1)
    for j in range(3):
        for n,v in enumerate([bits(float('inf')),bits(float('-inf')),bits(float('nan')),-1,bits(2.0)+1,bits(-2.0)+1,bits(float.fromhex('0x1.fffffffffffffp1023'))]):
            inputs=[bits(0.5)]*3;inputs[j]=v;add(f'input-{j}-{n}',unit,inputs,cancel=1)
    for n,m in enumerate([[0]*9,[1,2,3,2,4,6,1,0,1],unit]):
        for stop in (0,1,2,3):add(f'cancel-admission-{n}-{stop}',m,[bits(0.5)]*3,cancel=stop)
    assert len({c['name']for c in cases})==len(cases)
    return dict(schema=1,reference=dict(method='Exact Fraction Gaussian elimination on raw fixed-point matrices and exact input IEEE values; no cofactor oracle',criterion='At most two binary64 representable steps from correctly rounded exact solutions; exact signed projections and declared dyadic controls',seed=13694,Python=platform.python_version(),sha256=dict(generator=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),profile=hashlib.sha256(profile).hexdigest())),cases=cases)

if __name__=='__main__':
    out=ROOT/'tests/fixtures/icc-matrix-solve-reference.json.gz'
    out.write_bytes(gzip.compress((json.dumps(generate(),sort_keys=True,separators=(',',':'))+'\n').encode(),mtime=0))
    print(out,hashlib.sha256(out.read_bytes()).hexdigest())
