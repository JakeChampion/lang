"""Construct inverse-power observations with independent 90-digit Decimal mathematics."""
import argparse
from decimal import Decimal, localcontext
import gzip
import hashlib
import json
import math
from pathlib import Path
import random
import struct


def sha(data): return hashlib.sha256(data).hexdigest()
def bits(value): return struct.unpack('<q', struct.pack('<d', value))[0]
def value(raw): return struct.unpack('<d', struct.pack('<q', raw))[0]


def packet(case):
    return b'FPI1'+struct.pack('<iiiq',case['function_type'],case['cancel_at'],len(case['coefficients']),case['input_bits'])+struct.pack('<'+str(len(case['coefficients']))+'i',*case['coefficients'])


def expected(case):
    x=value(case['input_bits']);kind=case['function_type'];coefficients=case['coefficients']
    def error(name,polls=0):return dict(status='error',error=name,polls=polls)
    if not math.isfinite(x) or not 0<=x<=1:return error('Input')
    if not 0<=kind<=4:return error('Function')
    if kind!=0:return error('Unsupported')
    if len(coefficients)!=1:return error('Shape')
    if coefficients[0]<=0:return error('Domain')
    if case['cancel_at']in [1,2]:return error('Cancelled',case['cancel_at'])
    if coefficients[0]==65536 or x in [0,1]:return dict(status='ok',polls=2,decimal=str(Decimal.from_float(x)),exact_bits=bits(x))
    with localcontext()as context:
        context.prec=90
        result=context.power(Decimal.from_float(x),Decimal(65536)/Decimal(coefficients[0]))
    return dict(status='ok',polls=2,decimal=str(result))


def generate():
    rng=random.Random(13700);cases=[]
    def add(name,gamma,x,kind=0,coefficients=None,stop=0):
        c=dict(name=name,function_type=kind,coefficients=[gamma]if coefficients is None else coefficients,input_bits=bits(x),cancel_at=stop)
        c['expected']=expected(c);c['payload_sha256']=sha(packet(c));cases.append(c)
    inputs=[0.0,-0.0,math.ulp(0.0),math.nextafter(float.fromhex('0x1p-1022'),0),float.fromhex('0x1p-1022'),float.fromhex('0x1p-500'),0.125,0.25,1/3,0.5,0.75,math.nextafter(1.0,0.0),1.0]
    exponents=[1,2,3,255,256,257,32767,32768,32769,65535,65536,65537,131071,131072,131073,2147483646,2147483647]
    for gamma in exponents:
        for index,x in enumerate(inputs):add(f'power-{gamma}-{index}',gamma,x)
    for index in range(256):add(f'random-{index}',rng.randrange(1,2**31),rng.random())
    for gamma in [1,3,65535,65536,65537,2147483647]:
        for raw in [1,2,3,0x000ffffffffffffe,0x000fffffffffffff,0x0010000000000000,0x0010000000000001,0x3fdfffffffffffff,0x3fe0000000000000,0x3fe0000000000001,0x3feffffffffffffd,0x3feffffffffffffe,0x3fefffffffffffff]:
            add(f'ieee-{gamma}-{raw}',gamma,value(raw))
    for gamma in [1,65536,2147483647]:
        for stop in [1,2,3]:
            for index,x in enumerate([0.0,-0.0,0.5,1.0]):add(f'cancel-{gamma}-{stop}-{index}',gamma,x,stop=stop)
    for index,x in enumerate([-math.inf,math.inf,math.nan,-0.01,math.nextafter(1.0,math.inf)]):
        for kind in [-1,0,1,4,5]:add(f'input-{index}-{kind}',0,x,kind=kind,coefficients=[],stop=1)
    for kind in [-2**31,-1,5,2**31-1]:add(f'function-{kind}',65536,.5,kind=kind,stop=1)
    for kind in [1,2,3,4]:
        for coefficients in [[],[0],[65536]*7]:add(f'unsupported-{kind}-{len(coefficients)}',0,.5,kind=kind,coefficients=coefficients,stop=1)
    for size in [0,2,3,7,8]:add(f'shape-{size}',0,.5,coefficients=[0]*size,stop=1)
    for gamma in [-2**31,-1,0]:add(f'domain-{gamma}',gamma,.5,stop=1)
    assert len({c['name']for c in cases})==len(cases)
    return dict(schema=1,reference=dict(sha256=dict(generator=sha(Path(__file__).read_bytes())),construction='Independent inverse-power Decimal arithmetic at 90 digits over exact binary64 inputs and exact s15Fixed16 coefficients',criterion='Absolute error <=1e-12 for this finite corpus; exact identity and endpoint bits; not a universal bound',seed=13700,decimal_precision=90),cases=cases)


def main():
    parser=argparse.ArgumentParser();parser.add_argument('output',type=Path);options=parser.parse_args();d=generate();options.output.parent.mkdir(parents=True,exist_ok=True);options.output.write_bytes(gzip.compress(json.dumps(d,sort_keys=True,separators=(',',':')).encode(),mtime=0));print(json.dumps(dict(cases=len(d['cases']),sha256=sha(options.output.read_bytes()))))


if __name__=='__main__':main()
