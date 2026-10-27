"""Construct inverse observations from exact forward segments and 90-digit Decimal power."""
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


def sha(data): return hashlib.sha256(data).hexdigest()
def bits(value): return struct.unpack('<q', struct.pack('<d', value))[0]
def value(raw): return struct.unpack('<d', struct.pack('<q', raw))[0]

def packet(case):
    return b'FCI1'+struct.pack('<iiiiiq',case['kind'],case['max_samples'],case['cancel_at'],case['parameter'],len(case['values']),case['input_bits'])+struct.pack('<'+str(len(case['values']))+'i',*case['values'])

def admission(case):
    kind=case['kind']; maximum=case['max_samples']; values=case['values']; x=value(case['input_bits']); polls=0
    def error(name): return dict(status='error',error=name,polls=polls)
    if maximum<=0: return error('Limit')
    if not math.isfinite(x) or not 0<=x<=1: return error('Input')
    if kind==3: return error('Unsupported')
    if kind==1 and not 1<=case['parameter']<=65535: return error('Value')
    if kind==2:
        if len(values)>maximum or len(values)>65536: return error('Limit')
        if len(values)<2: return error('Shape')
    polls+=1
    if case['cancel_at']==polls: return error('Cancelled')
    if kind==2:
        for first in range(0,len(values),64):
            polls+=1
            if case['cancel_at']==polls: return error('Cancelled')
            if any(not 0<=v<=65535 for v in values[first:first+64]): return error('Value')
        up=all(a<=b for a,b in zip(values,values[1:])); down=all(a>=b for a,b in zip(values,values[1:]))
        if up==down: return error('NonInvertible')
    polls+=1
    if case['cancel_at']==polls: return error('Cancelled')
    return dict(status='ok',polls=polls)

def inverse_segments(values, input_value):
    # Enumerate forward segments rather than repeat the implementation's search.
    requested=Fraction(input_value)*65535
    target=min(max(requested,Fraction(min(values))),Fraction(max(values)))
    least=greatest=None; steps=len(values)-1
    for i,(a,b) in enumerate(zip(values,values[1:])):
        if a==b:
            if target!=a: continue
            left,right=Fraction(i,steps),Fraction(i+1,steps)
        elif min(a,b)<=target<=max(a,b):
            left=right=(i+(target-a)/(b-a))/steps
        else: continue
        least=left if least is None else min(least,left)
        greatest=right if greatest is None else max(greatest,right)
    assert least is not None and greatest is not None,'inverse segment intersection must exist'
    result=least if target==values[-1]else greatest
    assert isinstance(result,Fraction),'inverse reference must remain exact'
    identity=Fraction(input_value)==result and any(a*steps==i*65535 and (b-a)*steps==65535 and Fraction(i,steps)<=result<=Fraction(i+1,steps) for i,(a,b)in enumerate(zip(values,values[1:])))
    return result,identity

def expected(case, exact=False):
    result=admission(case)
    if result['status']=='error': return result
    x=value(case['input_bits']);kind=case['kind']
    if kind==0 or kind==1 and (case['parameter']==256 or x in [0,1]):
        f=Fraction(x);result.update(fraction=[str(f.numerator),str(f.denominator)],exact_bits=bits(x))
    elif kind==1:
        with localcontext()as context:
            context.prec=90
            result['decimal']=str(context.power(Decimal.from_float(x),Decimal(256)/Decimal(case['parameter'])))
    else:
        f,identity=inverse_segments(case['values'],x);result['fraction']=[str(f.numerator),str(f.denominator)]
        if identity: result['exact_bits']=bits(x)
        elif f in [0,1]or exact and Fraction(float(f))==f: result['exact_bits']=bits(float(f))
    return result

def generate():
    cases=[];rng=random.Random(13698)
    def add(name,kind,x,values=(),parameter=0,maximum=4096,stop=0,exact=False):
        c=dict(name=name,kind=kind,input_bits=bits(x),values=list(values),parameter=parameter,max_samples=maximum,cancel_at=stop)
        c['expected']=expected(c,exact);c['payload_sha256']=sha(packet(c));cases.append(c)
    inputs=[0.0,-0.0,math.ulp(0.0),math.nextafter(float.fromhex('0x1p-1022'),0),float.fromhex('0x1p-1022'),0.125,0.25,1/3,0.5,0.75,math.nextafter(1.0,0.0),1.0]
    for i,x in enumerate(inputs):add(f'identity-{i}',0,x)
    for gamma in [1,2,3,64,127,128,255,256,257,563,1024,4096,32767,65535]:
        for i,x in enumerate(inputs):add(f'gamma-{gamma}-{i}',1,x,parameter=gamma)
    for n in range(128):add(f'gamma-random-{n}',1,rng.random(),parameter=rng.randrange(1,65536))
    tables=[[0,65535],[65535,0],[0,0,65535],[0,65535,65535],[65535,65535,0],[65535,0,0],[16384,16384,32768,32768],[32768,32768,16384,16384],[0,21845,43690,65535]]
    for t,table in enumerate(tables):
        for i,x in enumerate(inputs):add(f'policy-{t}-{i}',2,x,table,exact=x in [0,.25,.5,.75,1])
    for wire in [1,2,3,257,16384,21845,32767,32768,43690,65534]:
        center=float(Fraction(wire,65535))
        for direction,table in enumerate([[0,wire,wire,65535],[65535,wire,wire,0]]):
            for side,x in enumerate([math.nextafter(center,0),center,math.nextafter(center,1)]):add(f'phase-{wire}-{direction}-{side}',2,x,table)
    for n in [2,3,4,63,64,65,127,128,129,4096,65536]:
        ascending=[i*65535//(n-1)for i in range(n)];randomized=sorted(rng.randrange(65536)for _ in range(n));flat=[12000]*(n//2)+[50000]*(n-n//2)
        for style,table in enumerate([ascending,ascending[::-1],randomized,randomized[::-1],flat,flat[::-1]]):
            selected=inputs if n<4096 else[0.0,1/3,0.5,1.0]
            for i,x in enumerate(selected):add(f'sampled-{n}-{style}-{i}',2,x,table,maximum=max(n,4096))
            for k in sorted({0,(n-1)//2,n-1}):
                center=float(Fraction(table[k],65535))
                for side,x in enumerate([math.nextafter(center,0),center,math.nextafter(center,1)]):add(f'knot-{n}-{style}-{k}-{side}',2,x,table,maximum=max(n,4096))
    for n in range(80):
        table=sorted(rng.randrange(65536)for _ in range(rng.randrange(2,257)))
        if n%2:table.reverse()
        add(f'random-sampled-{n}',2,rng.random(),table)
    for i,x in enumerate([-math.inf,math.inf,math.nan,-0.001,math.nextafter(1,math.inf)]):
        for kind in range(4):add(f'input-{i}-{kind}',kind,x,[0,65535],parameter=256,stop=1)
    for gamma in [-2**31,-1,0,65536,2**31-1]:add(f'gamma-value-{gamma}',1,.5,parameter=gamma,stop=1)
    for maximum in [-2**31,-1,0]:
        for kind in range(4):add(f'limit-{maximum}-{kind}',kind,.5,maximum=maximum,stop=1)
    for n in [0,1]:add(f'shape-{n}',2,.5,[1]*n,stop=1)
    for n,maximum in [(3,2),(4097,4096),(65537,2**31-1)]:add(f'sample-limit-{n}',2,.5,[0]*n,maximum=maximum,stop=1)
    for n,table in enumerate([[0,0],[1234]*3,[0,65535,0],[65535,0,65535],[0,1,0,2],[2,1,2,0]]):
        for stop in [0,1,2,3]:add(f'noninvertible-{n}-{stop}',2,.5,table,stop=stop)
    for k in [0,1,63,64,65,128]:
        table=list(range(129));table[k]=-1 if k%2 else 65536
        for stop in [0,1,2,3,4,5]:add(f'value-precedence-{k}-{stop}',2,.5,table,stop=stop)
    for function in range(5):add(f'parametric-{function}',3,.5,[65536],parameter=function)
    for n in [2,63,64,65,129]:
        for stop in range(1,4+(n+63)//64):add(f'cancel-samples-{n}-{stop}',2,.375,list(range(n)),stop=stop)
    for kind in [0,1]:
        for stop in [1,2,3]:add(f'cancel-{kind}-{stop}',kind,.5,parameter=563,stop=stop)
    for stop in [1,2,1024,1025,1026,1027]:add(f'maximum-cancel-{stop}',2,.375,list(range(65536)),maximum=65536,stop=stop)
    add('limit-before-input',0,math.nan,maximum=0,stop=1)
    add('input-before-shape',2,math.nan,[],stop=1)
    add('input-before-gamma-value',1,math.nan,parameter=0,stop=1)
    add('input-before-unsupported',3,math.nan,stop=1)
    assert len({c['name']for c in cases})==len(cases)
    mandatory=next(c for c in cases if c['name']=='phase-21845-0-1')
    assert mandatory['input_bits']==bits(1/3)and mandatory['expected'].get('exact_bits')==bits(1/3)
    return dict(schema=1,reference=dict(sha256=dict(generator=sha(Path(__file__).read_bytes())),criterion='Absolute error <=1e-12 for this finite corpus; exact declared identities, endpoints and dyadic controls; not a universal error bound',construction='Exact Fraction enumeration of forward segments with ICC Annex F.1 inverse selection; independent inverse-gamma Decimal power at 90 digits',seed=13698,decimal_precision=90),cases=cases)

def main():
    parser=argparse.ArgumentParser();parser.add_argument('output',type=Path);options=parser.parse_args();document=generate();options.output.parent.mkdir(parents=True,exist_ok=True);options.output.write_bytes(gzip.compress(json.dumps(document,sort_keys=True,separators=(',',':')).encode(),mtime=0));print(json.dumps(dict(cases=len(document['cases']),sha256=sha(options.output.read_bytes()))))

if __name__=='__main__':main()
