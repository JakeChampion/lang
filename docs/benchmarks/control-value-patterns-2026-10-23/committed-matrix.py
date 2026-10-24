from pathlib import Path
import os,subprocess,json,re
out=Path('/workspace/scratch/lang-grammar');root=Path('/workspace/lang-grammar-current')
cases={}
for name in ['basic','flow','string','array','nested','literal_once','enum_once','diverge','statement_arms']:
 folder=root/'conformance/cases'/('control_value_'+name)
 cases[name]=((folder/'main.fern').read_text(),(folder/'expected.stdout').read_text())

rows=[];fail=[]
env=dict(os.environ,FERN_STRICT_IR='1',FERN_SANITIZE='1',FERN_LEAKCHECK='1')
def run(name,args,stdout=None,census=False):
 p=subprocess.run(list(map(str,args)),cwd=root,env=env,capture_output=True,text=True,timeout=90)
 row=dict(name=name,args=list(map(str,args)),exit=p.returncode,stdout=p.stdout,stderr=p.stderr);rows.append(row)
 okay=p.returncode==0 and (stdout is None or p.stdout==stdout)
 if census:
  m=re.search(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',p.stderr);okay=okay and bool(m and m[1]==m[2] and m[3]=='0')
 if not okay:fail.append(row);print('FAIL',name,p.returncode,p.stdout,p.stderr[:1000],flush=True)
 (out/'committed-matrix-results.json').write_text(json.dumps(dict(results=rows,failures=fail),indent=2)+'\n')
 return p
for name,(src,stdout) in cases.items():
 source=out/('value-'+name+'.fern');source.write_text(src+'\n');formats=[]
 for cli in ['committed-oracle','committed-compiler']:
  for mode in ['-check','-interp']:
   run(name+'-'+cli+mode,[out/cli,mode,source,root/'internal/stdlib'],stdout if mode=='-interp' else '')
  f=run(name+'-'+cli+'-fmt',[out/cli,'-fmt',source]);formats.append(f.stdout)
  formatted=out/(name+'-'+cli+'-formatted.fern');formatted.write_text(f.stdout)
  again=run(name+'-'+cli+'-fmt-again',[out/cli,'-fmt',formatted])
  if again.stdout != f.stdout: fail.append({'name':name+'-'+cli+'-format-unstable'})
  run(name+'-'+cli+'-formatted-check',[out/cli,'-check',formatted,root/'internal/stdlib'],'')
  run(name+'-'+cli+'-formatted-interp',[out/cli,'-interp',formatted,root/'internal/stdlib'],stdout)

 if formats[0]!=formats[1]:fail.append({'name':name+'-format-diff','Go':formats[0],'self':formats[1]});print('FAIL format',name,flush=True)
 for target,runner in [('x86-64-linux',[]),('arm64-linux',['qemu-aarch64']),('wasm32-wasi',['wasmtime','run'])]:
  binary=out/('value-'+name+'-'+target);args=[out/'committed-compiler','-target',target,'-o',binary]
  if target=='wasm32-wasi':args+=['-emit','command-module']
  p=run(name+'-compile-'+target,args+[source,root/'internal/stdlib'],'')
  if p.returncode==0:run(name+'-run-'+target,runner+[binary],stdout,True)
 print('DONE',name,flush=True)
(out/'committed-matrix-results.json').write_text(json.dumps(dict(results=rows,failures=fail),indent=2)+'\n')
assert not fail,len(fail)
print('PASS',len(rows),'commands',flush=True)
