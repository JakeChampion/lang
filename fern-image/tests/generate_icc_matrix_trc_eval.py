"""Independent Fraction/Decimal composition vectors and frozen-profile CMM observations."""
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
from generate_icc_parametric_eval import reference as parametric_reference
from reference_icc_matrix_trc import observe, PROFILE_SHA256

ROOT = Path(__file__).resolve().parents[1]
IDENTITY_MATRIX = [65536, 0, 0, 0, 65536, 0, 0, 0, 65536]


def sha(data): return hashlib.sha256(data).hexdigest()
def bits(x): return struct.unpack('<q', struct.pack('<d', x))[0]
def value(raw): return struct.unpack('<d', struct.pack('<q', raw))[0]
def curve(kind=0, parameter=0, values=()): return dict(kind=kind, parameter=parameter, values=list(values))


def frozen_model():
    raw = (ROOT/'tests/fixtures/icc-feasibility-srgb.icc').read_bytes()
    assert sha(raw) == PROFILE_SHA256
    manifest = json.loads((ROOT/'tests/fixtures/icc-feasibility-reference.json').read_text())
    tags = {t['signature']: t for t in manifest['tags']}
    columns = [struct.unpack_from('>iii', raw, tags[s]['offset']+8) for s in ['rXYZ','gXYZ','bXYZ']]
    matrix = [columns[j][i] for i in range(3) for j in range(3)]
    curves = [curve(3, 3, struct.unpack_from('>5i', raw, tags[s]['offset']+12)) for s in ['rTRC','gTRC','bTRC']]
    return matrix, curves


def packet(case):
    frozen = int(case['frozen'])
    result = b'FTR1'+struct.pack('<iiiiiqqq',case['requested'],case['max_samples'],case['cancel_at'],frozen,
                               0 if frozen else len(case['matrix']),*case['input_bits'])
    if not frozen:
        result += struct.pack('<'+'i'*len(case['matrix']),*case['matrix'])
        for c in case['curves']:
            result += struct.pack('<iii',c['kind'],c['parameter'],len(c['values']))
            result += struct.pack('<'+'i'*len(c['values']),*c['values'])
    return result


def linear_reference(c, x):
    kind, parameter, values = c['kind'], c['parameter'], c['values']
    if kind == 3:
        return parametric_reference(parameter, values, x)
    if kind == 0 or kind == 1 and (parameter == 256 or x in (0,1)):
        f = Fraction(x)
        return dict(fraction=[str(f.numerator),str(f.denominator)],exact_bits=bits(x))
    if kind == 1:
        with localcontext() as context:
            context.prec=90; context.Emin=-999999999; context.Emax=999999999
            return dict(decimal=str(context.power(Decimal.from_float(x),Decimal(parameter)/256)))
    position = Fraction(x)*(len(values)-1)
    index = min(position.numerator//position.denominator,len(values)-2)
    f = (values[index]+(values[index+1]-values[index])*(position-index))/65535
    return dict(fraction=[str(f.numerator),str(f.denominator)])


def decimal_reference(reference):
    if 'fraction' in reference:
        a,b=map(int,reference['fraction']);return Decimal(a)/Decimal(b)
    return Decimal(reference['decimal'])


def expected(matrix, curves, inputs, cancel):
    polls = 4 + sum(2+(len(c['values'])+63)//64 if c['kind']==2 else 2 for c in curves)
    if 0 < cancel <= polls:
        return dict(status='error',error='Cancelled',polls=cancel)
    refs=[linear_reference(c,value(x))for c,x in zip(curves,inputs,strict=True)]
    with localcontext() as context:
        context.prec=90;context.Emin=-999999999;context.Emax=999999999
        linear=list(map(decimal_reference,refs))
        ideal=[str(sum((Decimal(matrix[3*i+j])*linear[j]/65536 for j in range(3)),Decimal(0)))for i in range(3)]
    exact=[]
    for i in range(3):
        row=matrix[3*i:3*i+3]
        if all('exact_bits'in r for r in refs):
            numbers=[value(r['exact_bits'])for r in refs]
            dot=sum((Fraction(c,65536)*Fraction(x)for c,x in zip(row,numbers)),Fraction())
            projection=[j for j in range(3)if row[j]==65536 and all(row[k]==0 for k in range(3)if k!=j)]
            exact.append(refs[projection[0]]['exact_bits']if projection else 0 if dot==0 else bits(float(dot))if Fraction(float(dot))==dot else None)
        else:exact.append(None)
    return dict(status='ok',polls=polls,linear=refs,ideal_xyz=ideal,exact_xyz_bits=exact)


def generate():
    cases=[];rng=random.Random(13683);profile_matrix,profile_curves=frozen_model()
    def add(name,matrix,curves,inputs,*,maximum=4096,requested=1,stop=0,error=None,polls=0,frozen=False,cmm=False):
        c=dict(name=name,matrix=list(matrix),curves=curves,input_bits=list(map(bits,inputs)),max_samples=maximum,
               requested=requested,cancel_at=stop,frozen=frozen,cmm_applicable=cmm)
        c['expected']=dict(status='error',error=error,polls=polls)if error else expected(c['matrix'],curves,c['input_bits'],stop)
        c['payload_sha256']=sha(packet(c));cases.append(c)
    identity=[curve()for _ in range(3)]
    edge=[-0.0,0.0,math.ulp(0.0),float.fromhex('0x1p-1022'),0.125,0.25,1/3,0.5,0.75,math.nextafter(1,0),1.0]
    for n,perm in enumerate(itertools.permutations(range(3))):
        matrix=[65536 if perm[i]==j else 0 for i in range(3)for j in range(3)]
        for j,x in enumerate(edge):add(f'identity-{n}-{j}',matrix,identity,[x,edge[(j+3)%len(edge)],edge[(j+7)%len(edge)]])
    for gamma in [1,128,255,256,257,563,1024,65535]:
        for j,x in enumerate(edge):
            for channel in range(3):
                curves=[curve(),curve(2,values=[65535,0]),curve(1,512)];curves[channel]=curve(1,gamma)
                add(f'gamma-{gamma}-{j}-{channel}',profile_matrix,curves,[x,edge[(j+3)%len(edge)],edge[(j+7)%len(edge)]])
    for n in [2,3,64,65,129,4096,65536]:
        for style in range(3):
            values=[i*65535//(n-1)for i in range(n)]if style==0 else[0 if i%2==0 else 65535 for i in range(n)]if style==1 else[rng.randrange(65536)for _ in range(n)]
            center=((n-1)//2)/(n-1)
            points=[0.,1/3,.5,1.,math.nextafter(center,0),center,math.nextafter(center,1)]
            for j,x in enumerate(points):
                curves=[curve(),curve(1,512),curve()];curves[n%3]=curve(2,values=values)
                inputs=[.25,.5,.75];inputs[n%3]=x
                add(f'samples-{n}-{style}-{j}',profile_matrix,curves,inputs,maximum=max(n,4096))
    for function in range(5):
        for gamma in [65536,65537,157286,2147483647]:
            pairs=[(65536,0)]if function==0 else[(65536,0),(65536,-16384),(2147450880,-2147385344)]
            for a,b in pairs:
                values=[gamma]if function==0 else[gamma,a,b]
                boundary=0.0 if function==0 else -b/a
                if function==2:values += [16384]
                if function>=3:
                    d=65536 if a>65536 else 32768;values += [5072,d];boundary=d/65536
                if function==4:values += [-16384,8192]
                points=edge+[math.nextafter(boundary,0),boundary,math.nextafter(boundary,1)]
                for j,x in enumerate(points):
                    if 0<=x<=1:
                        curves=[curve(1,512),curve(),curve(2,values=[0,65535])];curves[function%3]=curve(3,function,values)
                        inputs=[.25,.5,.75];inputs[function%3]=x
                        add(f'parametric-{function}-{gamma}-{a}-{b}-{j}',profile_matrix,curves,inputs)
    for function,values in [(0,[1]),(0,[32768]),(3,[65536,65536,0,65536,65536]),(4,[65536,65536,0,65536,65536,0,0])]:
        for j,x in enumerate(edge):add(f'selected-identity-{function}-{values[0]}-{j}',IDENTITY_MATRIX,[curve(3,function,values)]*3,[x]*3)
    for n in range(128):
        matrix=[rng.randrange(-2**31,2**31)for _ in range(9)]
        curves=[curve(1,rng.choice([1,256,512,65535])),curve(2,values=[0,65535,0]),curve(3,4,[131072,65536,0,65536,32768,-16384,8192])]
        add(f'signed-matrix-{n}',matrix,curves,[rng.random()for _ in range(3)])
    for n in range(64):
        a=rng.randrange(1,2**31);x=edge[n%len(edge)];z=edge[(n*7)%len(edge)]
        add(f'matrix-cancellation-{n}',[a,-a,rng.choice([-2147483648,-1,1,2147483647])]*3,identity,[x,x,z])
    points={}
    def frozen_add(values):points.setdefault(tuple(map(bits,values)),list(values))
    grid=[0.,1/255,1/16,1/4,1/2,3/4,15/16,254/255,1.]
    for values in itertools.product(grid,repeat=3):frozen_add(values)
    for i in range(256):frozen_add([i/255]*3)
    boundary=profile_curves[0]['values'][4]/65536
    for x in [math.nextafter(boundary,0),boundary,math.nextafter(boundary,1)]:
        for channel in range(3):
            for other in [0.,.5,1.]:
                inputs=[other]*3;inputs[channel]=x;frozen_add(inputs)
    cm_rng=random.Random(13659)
    for _ in range(256):frozen_add([cm_rng.getrandbits(53)/2**53 for _ in range(3)])
    cmm_inputs=list(points.values());observations,cmm_info=observe((ROOT/'tests/fixtures/icc-feasibility-srgb.icc').read_bytes(),cmm_inputs)
    for i,(inputs,xyz)in enumerate(zip(cmm_inputs,observations,strict=True)):
        add(f'frozen-cmm-{i}',profile_matrix,profile_curves,inputs,frozen=True,cmm=True)
        cases[-1]['expected']['cmm_xyz_bits']=list(map(bits,xyz))
    for n,maximum in enumerate([-2147483648,-1,0]):add(f'limit-{n}',IDENTITY_MATRIX,identity,[.5]*3,maximum=maximum,error='Limit')
    for request in [0,2,3]:add(f'intent-{request}',IDENTITY_MATRIX,identity,[.5]*3,requested=request,error='Intent')
    for count in [0,1,8,10]:add(f'matrix-shape-{count}',[0]*count,identity,[.5]*3,error='Shape')
    for channel in range(3):
        for n,x in enumerate([-.001,math.nextafter(1,math.inf),math.inf,-math.inf,math.nan]):
            inputs=[.5]*3;inputs[channel]=x;add(f'input-{channel}-{n}',IDENTITY_MATRIX,identity,inputs,error='Input')
        invalid=[(curve(1,p),'Curve.Value',0)for p in [-1,0,65536]]
        invalid += [(curve(2,values=v),'Curve.Shape',0)for v in [[],[1]]]
        invalid += [(curve(2,values=[0,65536]),'Curve.Value',2),(curve(2,values=[0]*64+[-1]),'Curve.Value',3)]
        invalid += [(curve(3,-1,[65536]),'Parametric.Function',0),(curve(3,5,[]),'Parametric.Function',0),(curve(3,0,[]),'Parametric.Shape',0),(curve(3,0,[0]),'Parametric.Domain',0),(curve(3,1,[32768,65536,0]),'Parametric.Unsupported',0),(curve(3,3,[65536,65536,-65536,65536,32768]),'Parametric.Domain',0)]
        for n,(bad,error,local_polls)in enumerate(invalid):
            curves=identity.copy();curves[channel]=bad;add(f'curve-error-{channel}-{n}',IDENTITY_MATRIX,curves,[.5]*3,error=error,polls=1+channel*2+local_polls)
    for n,maximum in [(2,1),(4097,4096),(65537,2147483647)]:add(f'sample-limit-{n}',IDENTITY_MATRIX,[curve(2,values=[0]*n),curve(),curve()],[.5]*3,maximum=maximum,error='Curve.Limit',polls=1)
    for n,curves in enumerate([identity,[curve(2,values=[0]*65),curve(),curve()],[curve(2,values=[0]*2),curve(2,values=[0]*65),curve(2,values=[0]*129)],profile_curves]):
        total=expected(IDENTITY_MATRIX,curves,[bits(.25),bits(.5),bits(.75)],0)['polls']
        for stop in range(1,total+2):add(f'cancel-{n}-{stop}',IDENTITY_MATRIX,curves,[.25,.5,.75],stop=stop)
    assert len({c['name']for c in cases})==len(cases)
    return dict(schema=1,reference=dict(method='Independent exact Fraction branches, 90-digit Decimal powers, and exact matrix dots of actual linear traces.',
                criterion=dict(linear_absolute='1e-12',matrix_steps=2,composed='row_L1*1e-12+B(A)',cmm_absolute='1e-6'),
                CMM=cmm_info,CMM_cases=len(cmm_inputs),Python=platform.python_version(),seed=13683,CMM_seed=13659,
                sha256=dict(generator=sha(Path(__file__).read_bytes()),parametric_reference=sha(Path(__file__).with_name('generate_icc_parametric_eval.py').read_bytes()),
                            CMM_reference=sha(Path(__file__).with_name('reference_icc_matrix_trc.py').read_bytes()),profile=PROFILE_SHA256,
                            profile_manifest=sha((ROOT/'tests/fixtures/icc-feasibility-reference.json').read_bytes()))),cases=cases)


if __name__=='__main__':
    output=ROOT/'tests/fixtures/icc-matrix-trc-reference.json.gz'
    output.write_bytes(gzip.compress((json.dumps(generate(),sort_keys=True,separators=(',',':'))+'\n').encode(),mtime=0))
    print(output,sha(output.read_bytes()))
