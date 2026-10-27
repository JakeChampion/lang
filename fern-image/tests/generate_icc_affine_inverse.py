"""Build inverse observations from clipped forward Decimal curves and monotonic bisection."""
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
def packet(c): return b'FAI1'+struct.pack('<iiiq',c['function_type'],c['cancel_at'],len(c['coefficients']),c['input_bits'])+struct.pack('<'+str(len(c['coefficients']))+'i',*c['coefficients'])


def admission(c):
    x=value(c['input_bits']);kind=c['function_type'];v=c['coefficients']
    if not math.isfinite(x)or not 0<=x<=1:return 'Input'
    if not 0<=kind<=4:return 'Function'
    if kind not in [1,2]:return 'Unsupported'
    if len(v)!=(3 if kind==1 else 4):return 'Shape'
    if v[0]<=0 or v[1]<=0:return 'Domain'
    offset=v[3]if kind==2 else 0
    if not 65536<=v[0]<=262144 or not 16384<=v[1]<=262144 or v[2]>0 or not 0<=offset<=32768:return 'Unsupported'
    if v[1]+v[2]<=0:return 'NonInvertible'
    if v[1]+v[2]>65536:return 'Unsupported'
    return None


def forward(v,x):
    g=Decimal(v[0])/65536;a=Decimal(v[1])/65536;b=Decimal(v[2])/65536;offset=Decimal(v[3])/65536 if len(v)==4 else Decimal(0)
    base=max(a*x+b,Decimal(0))
    return min(max(base**g+offset,Decimal(0)),Decimal(1))


def expected(c,exact=None):
    error=admission(c)
    if error:return dict(status='error',error=error,polls=0)
    if c['cancel_at']in [1,2]:return dict(status='error',error='Cancelled',polls=c['cancel_at'])
    v=c['coefficients'];x=value(c['input_bits']);raw_offset=v[3]if len(v)==4 else 0;offset=Fraction(raw_offset,65536)
    result=dict(status='ok',polls=2)
    if v[0]==65536 and v[1]==65536 and v[2]+raw_offset==0 and Fraction(x)>=offset:
        f=Fraction(x);result.update(fraction=[str(f.numerator),str(f.denominator)],exact_bits=bits(x));return result
    with localcontext()as context:
        context.prec=90
        requested=Decimal.from_float(x);first=forward(v,Decimal(0));last=forward(v,Decimal(1));assert first<last
        target=min(max(requested,first),last)
        lo=Decimal(0);hi=Decimal(1)
        for _ in range(112):
            mid=(lo+hi)/2;y=forward(v,mid)
            if (y>=target if target==last else y>target):hi=mid
            else:lo=mid
        # Exact flat and outer-range coordinates independently supplement the forward search.
        f=None
        if requested<=first:f=Fraction(-v[2],v[1])
        elif requested>=last and last<1:f=Fraction(1)
        if exact is not None:
            q=Fraction(exact)
            qd=Decimal(q.numerator)/Decimal(q.denominator)
            assert forward(v,qd)==target,'declared exact control must lie on the selected output'
            if f is not None:assert q==f,'declared exact flat coordinate'
            else:assert lo<=qd<=hi,'declared exact coordinate must meet the forward bisection'
            f=q
        if f is not None:
            result['fraction']=[str(f.numerator),str(f.denominator)]
            if f in [0,1]or exact is not None:
                assert Fraction(float(f))==f
                result['exact_bits']=bits(float(f))
        else:result['decimal']=str((lo+hi)/2)
    return result


def generate():
    rng=random.Random(13702);cases=[]
    def add(name,kind,v,x,stop=0,exact=None):
        c=dict(name=name,function_type=kind,coefficients=list(v),input_bits=bits(x),cancel_at=stop)
        c['expected']=expected(c,exact);c['payload_sha256']=sha(packet(c));cases.append(c)
    inputs=[-0.0,0.0,math.ulp(0.0),.25,.5,.75,math.nextafter(1.,0.),1.]
    for g in [65536,65537,131072,196608,262143,262144]:
        for a in [16384,65536,262144]:
            for white in [1,min(a,65536)]:
                for kind,c in [(1,0),(2,32768)]:
                    v=[g,a,white-a]+([c]if kind==2 else[]);label=f'{g}-{a}-{white}-{kind}'
                    for i,x in enumerate(inputs):add('grid-'+label+'-'+str(i),kind,v,x)
                    with localcontext()as context:
                        context.prec=90
                        knots=[float(forward(v,Decimal(0))),float(forward(v,Decimal(1)))]
                        threshold=Decimal(a-white)/Decimal(a)
                        middle=float(forward(v,(threshold+1)/2))
                    for knot,center in enumerate(knots):
                        for side,x in enumerate([math.nextafter(center,0.),center,math.nextafter(center,1.)]):add(f'knot-{label}-{knot}-{side}',kind,v,x)
                    add('forward-mid-'+label,kind,v,middle)
    for index in range(48):
        a=rng.randrange(16384,262145);white=rng.randrange(1,min(a,65536)+1);v=[rng.randrange(65536,262145),a,white-a,rng.randrange(32769)]
        with localcontext()as context:
            context.prec=90;threshold=Decimal(a-white)/Decimal(a);middle=float(forward(v,threshold+(1-threshold)*Decimal.from_float(rng.random())))
        for label,x in [('uniform',rng.random()),('forward',middle),('half',.5)]:add(f'random-{index}-{label}',2,v,x)
    for g in [65536,262144]:
        for a in [16384,16385,262143,262144]:
            for white in [1,min(a,65536)]:
                for c in [1,32767]:
                    v=[g,a,white-a,c];label=f'{g}-{a}-{white}-{c}'
                    with localcontext()as context:
                        context.prec=90
                        knots=[float(forward(v,Decimal(0))),float(forward(v,Decimal(1)))]
                        threshold=Decimal(a-white)/Decimal(a)
                        middle=float(forward(v,(threshold+1)/2))
                    for knot,center in enumerate(knots):
                        for side,x in enumerate([math.nextafter(center,0.),center,math.nextafter(center,1.)]):add(f'coefficient-edge-{label}-{knot}-{side}',2,v,x)
                    add('coefficient-edge-mid-'+label,2,v,middle)
    for c in [0,16384,32768]:
        for i,x in enumerate(inputs+[c/65536]):add(f'identity-{c}-{i}',2,[65536,65536,-c,c],x)
    for i,(kind,v,x,q)in enumerate([
        (1,[131072,65536,0],.25,Fraction(1,2)),
        (1,[131072,65536,-16384],.25,Fraction(3,4)),
        (2,[131072,65536,-16384,16384],.5,Fraction(3,4)),
        (2,[65536,65536,0,32768],1.,Fraction(1,2)),
        (2,[65536,131072,-65536,32768],.75,Fraction(5,8)),
        (2,[65536,131072,-65536,32768],1.,Fraction(3,4)),
    ]):add(f'exact-{i}',kind,v,x,exact=q)
    v=[262144,16384,-16383,32768]
    for label,x,q in [('below',math.nextafter(.5,0.),Fraction(16383,16384)),('at',.5,Fraction(16383,16384)),('above',math.nextafter(.5,1.),Fraction(1))]:add('mandatory-range-'+label,2,v,x,exact=q)
    for index,x in enumerate([-math.inf,math.inf,math.nan,-.01,math.nextafter(1.,math.inf)]):
        for kind in [-1,0,1,2,4,5]:add(f'input-{index}-{kind}',kind,[],x,stop=1)
    for kind in [-2**31,-1,5,2**31-1]:add('function-'+str(kind),kind,[],.5,stop=1)
    for kind in [0,3,4]:add('unsupported-'+str(kind),kind,[],.5,stop=1)
    for kind in [1,2]:
        for count in [0,1,2,5,8]:add(f'shape-{kind}-{count}',kind,[65536]*count,.5,stop=1)
    for i,v in enumerate([[0,65536,0],[-1,65536,0],[65536,0,0],[65536,-1,0],[65535,65536,0],[262145,65536,0],[65536,16383,0],[65536,262145,-196609],[65536,65536,1],[65536,131072,0],[65536,65536,-65536],[65536,65536,-65537]]):add(f'admission-{i}',1,v,.5,stop=1)
    for c in [-1,32769]:add('offset-'+str(c),2,[65536,65536,0,c],.5,stop=1)
    for profile,v in enumerate([[65536,65536,0,0],[131072,65536,0,32768],[262144,16384,-16383,32768]]):
        for stop in [1,2,3]:
            for i,x in enumerate([0.,.5,math.nextafter(.5,1.),1.]):add(f'cancel-{profile}-{stop}-{i}',2,v,x,stop=stop)
    assert len({c['name']for c in cases})==len(cases)
    return dict(schema=1,reference=dict(sha256=dict(generator=sha(Path(__file__).read_bytes())),construction='Clipped forward Decimal equations at 90 digits and 112 monotonic bisection steps, with exact Fraction flat/endpoint controls',criterion='Absolute error <=1e-12 for this finite corpus; exact declared identity/endpoint/dyadic bits; not a universal bound',seed=13702,decimal_precision=90,bisection_steps=112),cases=cases)


def main():
    parser=argparse.ArgumentParser();parser.add_argument('output',type=Path);args=parser.parse_args();d=generate();args.output.parent.mkdir(parents=True,exist_ok=True);args.output.write_bytes(gzip.compress(json.dumps(d,sort_keys=True,separators=(',',':')).encode(),mtime=0));print(json.dumps(dict(cases=len(d['cases']),sha256=sha(args.output.read_bytes()))))


if __name__=='__main__':main()
