from pathlib import Path
import subprocess,os,json,hashlib,datetime
repo=Path('/workspace/lang-performance-20261109-1950');out=repo/'build/postmerge-bootstrap-20261109-2007';statefile=out/'bootstrap.json';assert not statefile.exists()
source=subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip();assert subprocess.check_output(['git','status','--porcelain'],cwd=repo,text=True)==''
producer=repo/'build/bootstrap/stage0/stage0-20261107-38f241c/fern-selfhost-x86-64-linux';sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest();assert sha(producer)=='4698ff2c2123df02546ba2eb419e402018d59db811e75ce236269fd76786f0a3'
env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0')
state={'source':source,'producer_source':'38f241ca26366e5ef39a898c780bbfbc3a4694f5','producer_sha256':sha(producer),'debug':False,'certification':True,'stages':[],'claim':'Full self-host correctness controls. No timing or instruction comparison.'}
def save():statefile.write_text(json.dumps(state,indent=2)+'\n')
for n in [1,2,3]:
 b=out/('fern-stage'+str(n));args=[str(producer),'-target','x86-64-linux','-embed','internal/stdlib','-o',str(b),'compiler/fern.fern','internal/stdlib'];r={'stage':n,'args':args,'cwd':str(repo),'producer_sha256':sha(producer),'fern_env':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['stages'].append(r);save()
 with (out/('stage'+str(n)+'.stdout')).open('wb') as stdout,(out/('stage'+str(n)+'.log')).open('wb') as stderr:p=subprocess.run(args,cwd=repo,env=env,stdout=stdout,stderr=stderr)
 r.update(exit_code=p.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert p.returncode==0;r['sha256']=sha(b);save();print('stage',n,'exit0',r['sha256'],flush=True);producer=b
state['full_binaries_equal']=(out/'fern-stage2').read_bytes()==(out/'fern-stage3').read_bytes();save();assert state['full_binaries_equal'];assert subprocess.check_output(['git','status','--porcelain'],cwd=repo,text=True)=='';assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()==source;state['complete']=True;save();print('Complete stage2/stage3 binaries match.',flush=True)
