"""Construct independent clipped forward curves and inverse bisection observations."""
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
def bits(x): return struct.unpack('<q',struct.pack('<d',x))[0]
def value(raw): return struct.unpack('<d',struct.pack('<q',raw))[0]
def packet(c): return b'FQI1'+struct.pack('<iiiq',c['function_type'],c['cancel_at'],len(c['coefficients']),c['input_bits'])+struct.pack('<'+str(len(c['coefficients']))+'i',*c['coefficients'])


def admission(c):
    x=value(c['input_bits']);kind=c['function_type'];v=c['coefficients']
    if not math.isfinite(x)or not 0<=x<=1:return 'Input'
    if not 0<=kind<=4:return 'Function'
    if kind not in [3,4]:return 'Unsupported'
    if len(v)!=(5 if kind==3 else 7):return 'Shape'
    g,a,b,slope,d=v[:5];e,f=v[5:]if kind==4 else(0,0)
    if g<=0 or a<=0:return 'Domain'
    if g!=131072 or not 16384<=a<=262144 or not 16384<=slope<=262144 or b>0 or not 0<=d<=65536 or not 0<=e<=32768 or not 0<=f<=32768 or a%256 or d%256:return 'Unsupported'
    base=Fraction(a*d,65536)+b
    if base<0:return 'Domain'
    if not 0<a+b<=65536:return 'Unsupported'
    if base**2+e*65536!=slope*d+f*65536:return 'Unsupported'
    return None


def forward(v,x):
    a,b,c,d=map(lambda q:Decimal(q)/65536,v[1:5]);e,f=map(lambda q:Decimal(q)/65536,v[5:]if len(v)==7 else(0,0))
    y=(a*x+b)**2+e if x>=d else c*x+f
    return min(max(y,Decimal(0)),Decimal(1))


def expected(c,exact=None):
    error=admission(c)
    if error:return dict(status='error',error=error,polls=0)
    if c['cancel_at']in [1,2]:return dict(status='error',error='Cancelled',polls=c['cancel_at'])
    v=c['coefficients'];x=value(c['input_bits']);a,b,slope,d=v[1:5];e,f=v[5:]if len(v)==7 else(0,0)
    join=Fraction(slope*d+f*65536,65536**2);result=dict(status='ok',polls=2)
    if slope==65536 and f==0 and Fraction(x)<=join:
        q=Fraction(x);result.update(fraction=[str(q.numerator),str(q.denominator)],exact_bits=bits(x));return result
    with localcontext()as context:
        context.prec=90;requested=Decimal.from_float(x);first=forward(v,Decimal(0));last=forward(v,Decimal(1));assert first<last
        target=min(max(requested,first),last);lo=Decimal(0);hi=Decimal(1)
        for _ in range(112):
            mid=(lo+hi)/2
            if forward(v,mid)>=target:hi=mid
            else:lo=mid
        q=None
        if requested<=first:q=Fraction(0)
        elif requested>=last and last<1:q=Fraction(1)
        elif Fraction(x)==join:q=Fraction(d,65536)
        if exact is not None:
            declared=Fraction(exact);decimal=Decimal(declared.numerator)/Decimal(declared.denominator)
            assert forward(v,decimal)==target
            assert q==declared if q is not None else lo<=decimal<=hi
            q=declared
        if q is not None:
            result['fraction']=[str(q.numerator),str(q.denominator)]
            if q in [0,1]or Fraction(x)==join or exact is not None:
                assert Fraction(float(q))==q;result['exact_bits']=bits(float(q))
        else:result['decimal']=str((lo+hi)/2)
    return result


def generate():
    rng=random.Random(13704);cases=[]
    def add(name,kind,v,x,stop=0,exact=None):
        c=dict(name=name,function_type=kind,coefficients=list(v),input_bits=bits(x),cancel_at=stop)
        c['expected']=expected(c,exact);c['payload_sha256']=sha(packet(c));cases.append(c)
    parameters=set()
    for a in [16384,16640,32768,65536,65792,131072,262144]:
        for d in [0,256,16384,32768,49152,65280,65536]:
            for white in sorted({256,min(a,65536)}):
                b=white-a;n=a*d//65536+b
                if n<0:continue
                slopes={16384,16385,32768,65536,262144}
                if d and n*n%d==0:slopes.add(n*n//d)
                for slope in sorted(slopes):
                    for e in [0,1,16384,32767,32768]:
                        numerator=n*n+e*65536-slope*d
                        if numerator%65536:continue
                        f=numerator//65536
                        if 0<=f<=32768 and 16384<=slope<=262144:parameters.add((131072,a,b,slope,d,e,f))
    assert parameters
    for profile,v in enumerate(sorted(parameters)):
        kinds=[(4,v)]+([(3,v[:5])]if v[5:]==(0,0)else[])
        for kind,coefficients in kinds:
            label=f'{profile}-{kind}'
            with localcontext()as context:
                context.prec=90;join=float(forward(coefficients,Decimal(v[4])/65536));last=float(forward(coefficients,Decimal(1)))
                midpoint=float(forward(coefficients,(Decimal(v[4])/65536+1)/2))
            inputs=[-0.,0.,math.ulp(0.),.25,.5,.75,math.nextafter(1.,0.),1.,midpoint]
            for knot in [join,last]:inputs.extend([math.nextafter(knot,0.),knot,math.nextafter(knot,1.)])
            for index,x in enumerate(inputs):add(f'grid-{label}-{index}',kind,coefficients,x)
    for i,(kind,v,x,q)in enumerate([
        (3,[131072,65536,0,16384,16384],.03125,Fraction(1,8)),
        (3,[131072,65536,0,16384,16384],.25,Fraction(1,2)),
        (4,[131072,65536,0,32768,32768,32768,32768],.625,Fraction(1,4)),
        (4,[131072,65536,-32768,32768,32768,16384,0],.3125,Fraction(3,4)),
    ]):add(f'exact-{i}',kind,v,x,exact=q)
    # Exact continuous joins with unequal upper and lower offsets.
    special=[131072,65536,-32768,32768,32768,16384,0]
    for index in range(64):add(f'unequal-offset-{index}',4,special,rng.random())
    for profile,v in enumerate([[131072,65536,0,16384,16384],[131072,65536,0,32768,32768,32768,32768],[131072,262144,-196608,65536,65536]]):
        kind=3 if len(v)==5 else 4
        for stop in [1,2,3]:
            for i,x in enumerate([0.,.03125,.0625,.75,1.]):add(f'cancel-{profile}-{stop}-{i}',kind,v,x,stop=stop)
    for i,x in enumerate([-math.inf,math.inf,math.nan,-.01,math.nextafter(1.,math.inf)]):
        for kind in [-1,0,3,4,5]:add(f'input-{i}-{kind}',kind,[],x,stop=1)
    for kind in [-2**31,-1,5,2**31-1,0,1,2]:add(f'function-{kind}',kind,[],.5,stop=1)
    for kind in [3,4]:
        for count in [0,1,2,3,4,6,8]:add(f'shape-{kind}-{count}',kind,[65536]*count,.5,stop=1)
    base=[131072,65536,0,16384,16384]
    for at,values in [(0,[-2**31,0,65536,131071,131073,2**31-1]),(1,[-1,0,16383,16385,262145]),(2,[-2**31,-32768,1]),(3,[-1,0,16383,16385,262145]),(4,[-256,1,16385,65537])]:
        for raw in values:
            v=base.copy();v[at]=raw;add(f'admission-{at}-{raw}',3,v,.5,stop=1)
    for i,v in enumerate([[131072,65536,0,16384,16384,-1,0],[131072,65536,0,16384,16384,0,32769],[157286,62119,3417,5072,2651]]):add(f'excluded-{i}',3 if len(v)==5 else 4,v,.5,stop=1)
    assert len({c['name']for c in cases})==len(cases)
    return dict(schema=1,reference=dict(sha256=dict(generator=sha(Path(__file__).read_bytes())),construction='Clipped quadratic forward Decimal equations at 90 digits and 112 monotonic bisection steps, with exact Fraction joins and declared bits',criterion='Absolute error <=1e-12 for this finite corpus; exact declared bits; not a universal bound',seed=13704,decimal_precision=90,bisection_steps=112),cases=cases)


def main():
    parser=argparse.ArgumentParser();parser.add_argument('output',type=Path);args=parser.parse_args();data=generate();args.output.parent.mkdir(parents=True,exist_ok=True);args.output.write_bytes(gzip.compress(json.dumps(data,sort_keys=True,separators=(',',':')).encode(),mtime=0));print(json.dumps(dict(cases=len(data['cases']),sha256=sha(args.output.read_bytes()))))


if __name__=='__main__':main()
