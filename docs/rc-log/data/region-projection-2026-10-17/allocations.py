from pathlib import Path
import os,subprocess,json,re,hashlib
out=Path('/workspace/scratch/lang-certifier-region-final-alloc');out.mkdir(exist_ok=True)
producer='/workspace/scratch/fern-byte-copy-report'
roots={'base':Path('/workspace/lang-byte-copy'),'new':Path('/workspace/lang-certifier-nearest-load')}
frozen=Path('/workspace/lang-coreutils-parity');result={'producer':hashlib.sha256(Path(producer).read_bytes()).hexdigest(),'rows':[]}
for tag,root in roots.items():
 env=dict(os.environ,FERN_LEAKCHECK='1',FERN_RC_CERTIFY_MEMO='0');env.pop('FERN_RC_CERTIFY',None)
 p=subprocess.run([producer,'-target','x86-64-linux','-o',str(out/(tag+'-instrumented')),str(root/'compiler/fern.fern'),str(root/'internal/stdlib')],capture_output=True,env=env,timeout=180)
 (out/(tag+'-build.log')).write_bytes(p.stderr);assert p.returncode==0,(tag,p.returncode,p.stderr.decode())
for mode in ['disabled','enabled']:
 for tag in roots:
  env=dict(os.environ,FERN_RC_CERTIFY_MEMO='0');env.pop('FERN_LEAKCHECK',None);env.pop('FERN_RC_CERTIFY',None)
  if mode=='disabled':env['FERN_RC_CERTIFY']='0'
  asm=out/(tag+'-'+mode+'.s')
  p=subprocess.run([str(out/(tag+'-instrumented')),'-target','x86-64-linux','-emit','asm','-o',str(asm),str(frozen/'compiler/checker.fern'),str(frozen/'internal/stdlib')],capture_output=True,env=env,timeout=180)
  (out/(tag+'-'+mode+'.log')).write_bytes(p.stderr);assert p.returncode==0,(tag,mode,p.returncode,p.stderr.decode())
  m=re.search(rb'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',p.stderr);assert m,p.stderr.decode()
  allocs,frees,live=map(int,m.groups());assert allocs==frees and live==0,(tag,mode,allocs,frees,live)
  result['rows'].append({'compiler':tag,'certifier':mode,'allocs':allocs,'frees':frees,'live_bytes':live,'output_sha256':hashlib.sha256(asm.read_bytes()).hexdigest()})
  (out/'results.json').write_text(json.dumps(result,indent=2)+'\n');print(result['rows'][-1],flush=True)
assert len(set(x['output_sha256']for x in result['rows']))==1
