"""Preserve all published inverse vectors and select independent expectations for the new API."""
import argparse
import copy
import gzip
import hashlib
import json
from pathlib import Path
import struct

import generate_icc_curv_inverse as curv
import generate_icc_power_inverse as power
import generate_icc_affine_inverse as affine
import generate_icc_quadratic_inverse as quadratic


def sha(data): return hashlib.sha256(data).hexdigest()
def bits(value): return struct.unpack('<q',struct.pack('<d',value))[0]
def canonical(data): return json.dumps(data,sort_keys=True,separators=(',',':')).encode()
def packet(case):
    return b'FDI1'+struct.pack('<iiiiiq',case['kind'],case['max_samples'],case['cancel_at'],case['parameter'],len(case['values']),case['input_bits'])+struct.pack('<'+str(len(case['values']))+'i',*case['values'])


def expected(case):
    if case['kind']==3:
        scalar=dict(function_type=case['parameter'],coefficients=case['values'],input_bits=case['input_bits'],cancel_at=case['cancel_at'])
        module=affine if case['parameter']in [1,2]else quadratic if case['parameter']in [3,4]else power
        direct=module.expected(scalar)
    else:direct=curv.expected(case)
    dispatch=dict(status='error',error='Limit',polls=0)if case['max_samples']<=0 else copy.deepcopy(direct)
    return dict(dispatch=dispatch,direct=direct)


def generate():
    folder=Path(__file__).parent;cases=[];origins={}
    for name,module,count in [('curv',curv,1974),('power',power,640),('affine',affine,1581),('quadratic',quadratic,5780)]:
        bundle=folder/'fixtures'/f'icc-{name}-inverse-reference.json.gz'
        source=json.loads(gzip.decompress(bundle.read_bytes()));assert len(source['cases'])==count
        generator=Path(module.__file__);assert source['reference']['sha256']['generator']==sha(generator.read_bytes())
        numeric=0
        for old in source['cases']:
            if name=='curv':
                case={k:copy.deepcopy(old[k])for k in ['kind','max_samples','cancel_at','parameter','values','input_bits']}
                same_route=old['kind']!=3
            else:
                case=dict(kind=3,max_samples=4096,cancel_at=old['cancel_at'],parameter=old['function_type'],values=copy.deepcopy(old['coefficients']),input_bits=old['input_bits'])
                same_route=(name=='power'and case['parameter']==0)or(name=='affine'and case['parameter']in[1,2])or(name=='quadratic'and case['parameter']in[3,4])
            case['name']=name+'/'+old['name'];case['expected']=expected(case)
            if same_route:
                # Preserve every published declared-bit control, not just its approximate numeric value.
                case['expected']['direct']=copy.deepcopy(old['expected'])
                case['expected']['dispatch']=copy.deepcopy(old['expected'])
            if old['expected']['status']=='ok':
                assert same_route and case['expected']['dispatch']==old['expected'];numeric+=1
            case['origin']=dict(bundle=name,name=old['name'],canonical_sha256=sha(canonical(old)),original_expected=copy.deepcopy(old['expected']))
            case['payload_sha256']=sha(packet(case));cases.append(case)
            # Reconstruct the entire original vector for a byte-independent preservation assertion.
            restored=copy.deepcopy(old)
            if name=='curv':
                for key in ['kind','max_samples','cancel_at','parameter','values','input_bits']:restored[key]=case[key]
            else:
                restored.update(function_type=case['parameter'],coefficients=case['values'],input_bits=case['input_bits'],cancel_at=case['cancel_at'])
            restored['expected']=case['origin']['original_expected'];assert restored==old
        origins[name]=dict(bundle=str(bundle.relative_to(folder)),bundle_sha256=sha(bundle.read_bytes()),generator=generator.name,generator_sha256=sha(generator.read_bytes()),cases=count,numeric=numeric,all_original_vectors_and_expectations_preserved=True)
    def add(name,kind,parameter,values,x,maximum=4096,stop=0):
        case=dict(name='common/'+name,kind=kind,parameter=parameter,values=list(values),input_bits=bits(x),max_samples=maximum,cancel_at=stop)
        case['expected']=expected(case);case['payload_sha256']=sha(packet(case));cases.append(case)
    for kind,parameter,values in [(0,0,[]),(1,0,[]),(2,0,[]),(3,-1,[]),(3,0,[65536]),(3,1,[131072,65536,0]),(3,2,[131072,65536,0,32768]),(3,3,[131072,65536,0,16384,16384]),(3,4,[131072,65536,0,32768,32768,32768,32768])]:
        for maximum in [-1,0,1,4096]:
            for ix,x in enumerate([float('nan'),0.5]):
                add(f'{kind}-{parameter}-{maximum}-{ix}',kind,parameter,values,x,maximum=maximum,stop=1 if ix==0 else 0)
    for kind,parameter,values in [(0,0,[]),(1,256,[]),(2,0,[0,65535]),(3,0,[65536]),(3,1,[65536,65536,0]),(3,2,[65536,65536,0,0]),(3,3,[131072,262144,-196608,65536,65536]),(3,4,[131072,262144,-196608,65536,65536,0,0])]:
        for ix,x in enumerate([-0.,0.,float.fromhex('0x0.0000000000001p-1022'),0.5,1.]):add(f'identity-{kind}-{parameter}-{ix}',kind,parameter,values,x)
    assert len({c['name']for c in cases})==len(cases)
    assert sum(v['cases']for v in origins.values())==9975
    return dict(schema=1,reference=dict(sha256=dict(generator=sha(Path(__file__).read_bytes())),origins=origins,construction='All 9975 published inverse vectors and original expectations, with explicit routing-context expectations; independent Fraction forward segments and 90-digit Decimal references remain unchanged',criterion='Absolute error <=1e-12 for each finite numerical observation and every declared exact-bit control; dispatcher and selected standalone successes must match bits; no Numeric waiver or universal bound'),cases=cases)


def main():
    parser=argparse.ArgumentParser();parser.add_argument('output',type=Path);args=parser.parse_args();data=generate();args.output.parent.mkdir(parents=True,exist_ok=True);args.output.write_bytes(gzip.compress(canonical(data),mtime=0));print(json.dumps(dict(cases=len(data['cases']),sha256=sha(args.output.read_bytes()),origins=data['reference']['origins'])))


if __name__=='__main__':main()
