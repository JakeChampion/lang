"""Build exact model observations from declared profile components and mutations."""
import base64
import gzip
import hashlib
import json
from pathlib import Path
import platform
import random
import struct

ROOT = Path(__file__).resolve().parents[1]
PROFILE_SHA = '3bccbfedf06978c3035b255167afcf462a50b27805096cee038ff4dd69c650a6'
DEFAULT = [1048576, 128, 1048576, 4096]
REQUIRED = ['desc','cprt','wtpt','rXYZ','gXYZ','bXYZ','rTRC','gTRC','bTRC']
PARAMETERS = [[65536], [65536,65536,0], [65536,65536,0,0], [65536,65536,0,65536,32768], [65536,65536,0,65536,32768,0,0]]

def sig(s): return int.from_bytes(s.encode('ascii'),'big')
def sha(data): return hashlib.sha256(data).hexdigest()
def fixed(values): return b'XYZ '+bytes(4)+struct.pack('>iii',*values)
def mluc(text):
    data=text.encode('utf-16-be')
    return b'mluc'+bytes(4)+struct.pack('>II',1,12)+b'enUS'+struct.pack('>II',len(data),28)+data

def curve(kind, values=(), function=0):
    return dict(kind=kind,values=list(values),function=function)
def curve_bytes(c):
    v=c['values'];k=c['kind']
    if k==0:return b'curv'+bytes(8)
    if k in [1,2]:return b'curv'+bytes(4)+struct.pack('>I',len(v))+struct.pack('>'+'H'*len(v),*v)
    return b'para'+bytes(4)+struct.pack('>HH',c['function'],0)+struct.pack('>'+'i'*len(v),*v)
def curve_tokens(c):return [c['kind'],c['function'],len(c['values']),*c['values']]
def curve_polls(c):return 4+(2*((len(c['values'])+63)//64)if c['kind']==2 else len(c['values'])if c['kind']==3 else 0)
def envelope_polls(n):return 3+2*n+sum((i+63)//64 for i in range(n))

def components(columns=None, curves=None):
    columns=columns or [[65536,0,0],[0,65536,0],[0,0,65536]]
    curves=curves or [curve(0),curve(1,[512]),curve(2,[0,32768,65535])]
    entries=[('desc',mluc('Constructed matrix data')),('cprt',mluc('No copyright, test reference')),('wtpt',fixed([63190,65536,54061]))]
    entries += [(name,fixed(col))for name,col in zip(['rXYZ','gXYZ','bXYZ'],columns)]
    entries += [(name,curve_bytes(c))for name,c in zip(['rTRC','gTRC','bTRC'],curves)]
    return entries,columns,curves

def build(entries, columns, curves, cls=1, preferred=0, directory=None, physical=None, aliases=True, flags=0, attributes_high=0, attributes_low=0, created=None, profile_id=None):
    n=len(entries);directory=list(range(n))if directory is None else directory
    physical=list(range(n))if physical is None else physical
    header=bytearray(132+n*12);struct.pack_into('>I',header,8,0x04400000)
    header[12:16]=b'mntr'if cls else b'scnr';header[16:24]=b'RGB XYZ '
    date=[2026,10,27,0,0,0]if created is None else created;struct.pack_into('>6H',header,24,*date);header[36:40]=b'acsp'
    struct.pack_into('>I',header,64,preferred);struct.pack_into('>iii',header,68,63190,65536,54061)
    profile_id=bytes(range(16))if profile_id is None else profile_id
    assert len(profile_id)==16
    header[84:100]=profile_id;struct.pack_into('>I',header,44,flags);struct.pack_into('>II',header,56,attributes_high,attributes_low);struct.pack_into('>I',header,128,n)
    positions={};unique={};output=header
    for i in physical:
        data=entries[i][1];key=data if aliases else (i,data)
        if key not in unique:
            unique[key]=len(output);output+=data+bytes((-len(data))%4)
        positions[i]=(unique[key],len(data))
    tags=[]
    for slot,i in enumerate(directory):
        name,data=entries[i];offset,size=positions[i]
        tag=[sig(name),offset,size,int.from_bytes(data[:4],'big')];tags.append(tag)
        struct.pack_into('>III',output,132+slot*12,*tag[:3])
    struct.pack_into('>I',output,0,len(output))
    tokens=[len(output),cls,preferred,1,len(unique),*date,flags,attributes_high,attributes_low,*profile_id,n]
    tokens += [v for tag in tags for v in tag]
    tokens += [columns[j][i]for i in range(3)for j in range(3)]+[63190,65536,54061]
    tokens += [v for c in curves for v in curve_tokens(c)]
    polls=envelope_polls(n)+(n+63)//64+8+sum(map(curve_polls,curves))+1
    return bytes(output),dict(tokens=tokens,polls=polls,tags=tags,n=n,columns=columns,curves=curves)

def packet(case):return b'FMD1'+struct.pack('<6i',case['requested'],*case['limits'],case['cancel_at'])+base64.b64decode(case['input_base64'])
def generate():
    cases=[]
    def add(name,data,meta=None,error=None,polls=None,requested=1,limits=None,cancel=0):
        expected=dict(status='ok',polls=meta['polls'],values=meta['tokens'])if error is None else dict(status='error',polls=polls,error=error)
        if cancel and cancel<=expected['polls']:expected=dict(status='error',error='Cancelled',polls=cancel)
        c=dict(name=name,input_base64=base64.b64encode(data).decode(),requested=requested,limits=DEFAULT.copy()if limits is None else limits,cancel_at=cancel,expected=expected)
        c['payload_sha256']=sha(packet(c));cases.append(c)
    entries,columns,curves=components();data,meta=build(entries,columns,curves)
    add('constructed-default',data,meta)
    for cls in [0,1]:
        for preferred in range(4):
            for alias in [False,True]:
                es,co,cs=components(curves=[curve(0)]*3);b,m=build(es,co,cs,cls,preferred,aliases=alias);add(f'class-{cls}-preferred-{preferred}-alias-{alias}',b,m)
    for i in range(9):
        b,m=build(entries,columns,curves,cls=i%2,preferred=i%4,flags=[0,1,2,3,0xffff0003][i%5],attributes_high=[0,0xffffffff,0x80000000][i%3],attributes_low=[0,1,2,4,8,15][i%6],created=[2024,2,29,23,i,59-i],profile_id=bytes((131+i+j)%256 for j in range(16)))
        add('metadata-fields-'+str(i),b,m)
    rng=random.Random(13674)
    for i in range(48):
        cols=[[rng.randrange(-2**31,2**31)for _ in range(3)]for _ in range(3)]
        cs=[curve(3,PARAMETERS[(i+j)%5],(i+j)%5)for j in range(3)]
        es,co,cs=components(cols,cs);order=list(range(9));rng.shuffle(order);physical=order[::-1];b,m=build(es,co,cs,cls=i%2,preferred=i%4,directory=order,physical=physical);add(f'permutation-signed-{i}',b,m)
    for count in [2,3,63,64,65,4096,65536]:
        cs=[curve(2,[(j*32771)%65536 for j in range(count)])]*3;es,co,cs=components(curves=cs);b,m=build(es,co,cs)
        cap=65536 if count>4096 else 4096;lim=[1048576,128,1048576,cap];add(f'samples-{count}',b,m,limits=lim)
        for stop in [1,envelope_polls(9)+1,m['polls']//2,m['polls'],m['polls']+1]:add(f'samples-{count}-cancel-{stop}',b,m,limits=lim,cancel=stop)
    for n in [0,1,54,55,56,119,120,121,1015]:
        es=entries+[(chr(122)+format(i,'03x'),b'zzzz'+bytes(4))for i in range(n)]
        b,m=build(es,columns,curves);add(f'extra-tags-{n}',b,m,limits=[1048576,1024,1048576,4096])
    for i,name in enumerate(REQUIRED):
        es=[(key if key!=name else 'zzzz',blob)for key,blob in entries];b,m=build(es,columns,curves)
        prior={'desc':0,'cprt':0,'rXYZ':0,'gXYZ':2,'bXYZ':4,'wtpt':6,'rTRC':8,'gTRC':8+curve_polls(curves[0]),'bTRC':8+curve_polls(curves[0])+curve_polls(curves[1])}[name]
        add('missing-'+name,b,error='MissingTag',polls=envelope_polls(9)+1+prior)
    for name in ['desc','cprt']:
        es=[(key,b'text'+blob[4:]if key==name else blob)for key,blob in entries];b,m=build(es,columns,curves);add('metadata-type-'+name,b,error='MetadataType',polls=envelope_polls(9)+1)
    for name in ['rXYZ','gXYZ','bXYZ','wtpt']:
        for kind in ['type','length']:
            es=[(key,(b'sf32'+blob[4:]if kind=='type'else blob[:-4])if key==name else blob)for key,blob in entries];b,m=build(es,columns,curves);prior=['rXYZ','gXYZ','bXYZ','wtpt'].index(name)*2
            add(f'xyz-{name}-{kind}',b,error='Tag.Signature'if kind=='type'else'Tag.Length',polls=envelope_polls(9)+1+prior)
    for name in ['A2B0','A2B1','A2B2','D2B0','D2B1','D2B2','D2B3']:
        for index in [0,4,9]:
            es=entries.copy();es.insert(index,(name,b'mft2'+bytes(4)));b,m=build(es,columns,curves);add(f'forward-{name}-{index}',b,error='UnsupportedTag',polls=envelope_polls(10)+1)
    for name in ['B2A0','B2A1','B2A2','B2D0','B2D1','B2D2','B2D3','chad']:
        es=entries+[(name,b'sf32'+bytes(4))];b,m=build(es,columns,curves);add('opaque-'+name,b,m)
    bad_curves=[('gamma-zero',curve(1,[0]),'CurveDomain'),('power-zero',curve(3,[0]),'CurveDomain'),('affine-fractional',curve(3,[32768,65536,0],1),'UnsupportedCurve'),('affine-zero',curve(3,[65536,0,0],1),'CurveDomain'),('white-outside',curve(3,[65536,65536,1],1),'UnsupportedCurve'),('lower-negative',curve(3,[65536,65536,-32768,0,16384],3),'CurveDomain')]
    for label,bad,error in bad_curves:
        for channel in range(3):
            cs=curves.copy();cs[channel]=bad;es,co,cs=components(curves=cs);b,m=build(es,co,cs)
            read_polls=2+(len(bad['values'])if bad['kind']==3 else 0)
            add(f'curve-{label}-{channel}',b,error=error,polls=envelope_polls(9)+1+8+sum(map(curve_polls,cs[:channel]))+read_polls)
    for request in [0,2,3]:add('request-'+str(request),data,error='Intent',polls=0,requested=request)
    for i in range(4):
        for v in [0,-1]:
            lim=DEFAULT.copy();lim[i]=v;add(f'limit-{i}-{v}',data,error='Limit',polls=0,limits=lim)
    for field,limit in [(0,len(data)-1),(1,8)]:
        lim=DEFAULT.copy();lim[field]=limit;add('envelope-budget-'+str(field),data,error='Envelope.Limit',polls=0,limits=lim)
    lim=DEFAULT.copy();lim[2]=12;add('curve-byte-budget',data,error='Tag.Limit',polls=envelope_polls(9)+1+8+curve_polls(curves[0]),limits=lim)
    lim=DEFAULT.copy();lim[3]=1;add('curve-sample-budget',data,error='Tag.Limit',polls=envelope_polls(9)+1+8+curve_polls(curves[0])+curve_polls(curves[1]),limits=lim)
    cs=[curve(2,[0]*65537),curve(0),curve(0)];es,co,cs=components(curves=cs);b,m=build(es,co,cs);add('hard-sample-count',b,error='Tag.Limit',polls=envelope_polls(9)+1+8,limits=[1048576,128,1048576,65537])
    es=entries+[(chr(122)+format(i,'03x'),b'zzzz'+bytes(4))for i in range(1016)];b,m=build(es,columns,curves);add('hard-tag-count',b,error='Envelope.Limit',polls=0,limits=[1048576,1025,1048576,4096])
    for at,value,error in [(36,0,'Signature'),(8,0x04300000,'Version'),(12,0,'DeviceClass'),(16,0,'ColorSpace'),(20,0,'Pcs'),(100,1,'Header')]:
        b=bytearray(data);struct.pack_into('>I',b,at,value);add('envelope-header-'+error,bytes(b),error='Envelope.'+error,polls=1)
    for cut in range(132):add('prefix-'+str(cut),data[:cut],error='Envelope.Truncated',polls=0)
    for stop in range(1,meta['polls']+2):add('every-default-cancel-'+str(stop),data,meta,cancel=stop)
    frozen=(ROOT/'tests/fixtures/icc-feasibility-srgb.icc').read_bytes();assert sha(frozen)==PROFILE_SHA
    # Fixed known-good fixture fields. No malformed-profile classifier uses this extraction.
    n=11;tags=[list(struct.unpack_from('>III',frozen,132+12*i))+[int.from_bytes(frozen[struct.unpack_from('>I',frozen,136+12*i)[0]:][:4],'big')]for i in range(n)]
    date=list(struct.unpack_from('>6H',frozen,24));cols=[[28576,14581,912],[25239,46983,6361],[9375,3972,46787]];cs=[curve(3,[157286,62119,3417,5072,2651],3)]*3
    tokens=[588,1,0,1,9,*date,0,0,0,*frozen[84:100],n]+[v for t in tags for v in t]+[cols[j][i]for i in range(3)for j in range(3)]+[63190,65536,54061]+[v for c in cs for v in curve_tokens(c)]
    add('frozen-profile',frozen,dict(tokens=tokens,polls=envelope_polls(n)+1+8+sum(map(curve_polls,cs))+1))
    assert len({c['name']for c in cases})==len(cases)
    return dict(schema=1,reference=dict(method='Exact integers from declared profile components, layout permutations, and explicitly classified mutations; fixed frozen profile metadata',seed=13674,Python=platform.python_version(),Pillow_metadata_applicability='Frozen profile header, columns, and media white only; no transform or negative-policy oracle',Pillow_version='12.3.0',LittleCMS_version='2.19',offline_metadata=dict(device_class='mntr',color_space='RGB ',PCS='XYZ ',preferred_intent=0,columns=[[28576,14581,912],[25239,46983,6361],[9375,3972,46787]],media_white=[63190,65536,54061]),sha256=dict(generator=sha(Path(__file__).read_bytes()),profile=PROFILE_SHA)),cases=cases)

if __name__=='__main__':
    out=ROOT/'tests/fixtures/icc-matrix-model-reference.json.gz';d=generate();out.write_bytes(gzip.compress((json.dumps(d,sort_keys=True,separators=(',',':'))+'\n').encode(),mtime=0));print(len(d['cases']),sha(out.read_bytes()))
