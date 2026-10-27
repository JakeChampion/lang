from pathlib import Path
import subprocess,json,hashlib,os,re,sys,shutil
root=Path('/workspace/lang-image-icc-matrix-trc');mode=sys.argv[1];compiler=Path('/workspace/image-coverage-current-fern-82ba47cad');prefix=Path('/workspace/image-icc-composition-committed-'+mode)
assert not prefix.with_suffix('.json').exists()
assert not subprocess.check_output(['git','status','--porcelain'],cwd=root)
source='tests/icc_matrix_trc_eval_test.fern';names=subprocess.check_output(['git','ls-files','compiler','internal/stdlib','fern-image'],cwd=root,text=True).splitlines()+['fern-image/packages/codecs/icc/matrix_trc_eval.fern','fern-image/'+source]
names=sorted(set(p for p in names if p.endswith(('.fern','.fern.md'))))
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def hashes():return {p:sha(root/p) for p in names}
subprocess.run(['git','diff','--exit-code','82ba47cad60c55a2c0649147afd0ff40802e1c4d','HEAD','--','compiler','internal/stdlib'],cwd=root,check=True)
assert sha(compiler)=='6e95755acdba854fc94ec32aaa37d09dd8264bf4438b9952e37cc198bac5d9ab'
env=dict(os.environ,FERN_SANITIZE='1',FERN_LEAKCHECK='1');before=hashes();tools={str(compiler):sha(compiler)};commands=[]
(root/'fern-image/build').mkdir(exist_ok=True)
if mode=='go':
 oracle=Path('/workspace/image-icc-parametric-final-go-oracle');assert sha(oracle)=='7f82247e64bc635e6261d4d2b79ec0a4476b8858358e905cf64d98b106d3cd6b'
 changed=subprocess.check_output(['git','diff','--name-only','103243525a4987f48607dd0e7406c02370d6b1c3','HEAD','--','cmd','internal'],cwd=root,text=True).splitlines();assert all(p.startswith('internal/testing/')for p in changed),changed
 tools[str(oracle)]=sha(oracle);commands=[[str(oracle),'-check',source],[str(oracle),'-interp',source]]
else:
 target={'native':'x86-64-linux','wasm':'wasm32-wasi','arm64':'arm64-linux'}[mode];binary=root/('fern-image/build/icc-composition-initial-'+mode+('.wasm'if mode=='wasm'else'.elf'))
 commands=[[str(compiler),'-check',source],[str(compiler),'-target',target,'-sanitize',*(['-emit','core-module']if mode=='wasm'else[]),'-o',str(binary),source]]
 runner=[]if mode=='native'else[shutil.which('wasmtime'),'run']if mode=='wasm'else['/workspace/image-test-tools/qemu10/usr/bin/qemu-aarch64']
 if runner:tools[runner[0]]=sha(Path(runner[0]));commands.append(runner+[str(binary)])
 else:commands.append([str(binary)])
record={'source_base':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'feature_uncommitted':False,'mode':mode,'source_hashes':before,'tool_hashes':tools,'commands':[]}
with prefix.with_suffix('.log').open('w')as log:
 for cmd in commands:
  result=subprocess.run(cmd,cwd=root/'fern-image',env=env,stdout=log,stderr=subprocess.STDOUT);record['commands'].append({'command':cmd,'exit':result.returncode})
  if result.returncode:break
s=prefix.with_suffix('.log').read_text();heaps=[tuple(map(int,h))for h in re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',s)]
record.update(exit=result.returncode,TAP_ok=sum(x.startswith('ok ')for x in s.splitlines()),TAP_not_ok=sum(x.startswith('not ok ')for x in s.splitlines()),heaps=heaps,sources_unchanged=before==hashes()and all(sha(Path(p))==v for p,v in tools.items()))
prefix.with_suffix('.json').write_text(json.dumps(record,indent=2)+'\n');print({k:v for k,v in record.items()if k not in ['source_hashes','tool_hashes','commands']})
assert result.returncode==0 and record['TAP_ok']==6 and record['TAP_not_ok']==0 and record['sources_unchanged']
if mode!='go':assert len(heaps)==1 and all(a>0 and a==f and live==0 for a,f,live in heaps)
