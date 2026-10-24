from pathlib import Path
import os,subprocess,json,re,hashlib
root=Path('/workspace/lang-lossless-widening-current');out=Path('/workspace/scratch/lang-lossless-widening');work=out/'committed-matrix';work.mkdir(exist_ok=True)
selfcli=out/'committed-validation/bootstrap/stage3';oracle=out/'committed-oracle';assert selfcli.exists() and oracle.exists()
env=dict(os.environ,FERN_SANITIZE='1',FERN_LEAKCHECK='1',FERN_STRICT_IR='1');rows=[];fail=[]
report=dict(core_commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),environment={k:env[k] for k in ['FERN_SANITIZE','FERN_LEAKCHECK','FERN_STRICT_IR']},binaries={str(p):dict(bytes=p.stat().st_size,sha256=hashlib.sha256(p.read_bytes()).hexdigest()) for p in [selfcli,oracle]},commands=rows,failures=fail)
def run(name,args,stdout=None,exit=0,census=False,error=None):
 p=subprocess.run(list(map(str,args)),cwd=root,env=env,capture_output=True,text=True,timeout=90);good=(p.returncode==exit and (stdout is None or p.stdout==stdout)) if error is None else p.returncode!=0 and error in p.stderr
 if census:
  m=re.search(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',p.stderr);good=good and bool(m and m[1]==m[2] and m[3]=='0')
 row=dict(name=name,args=list(map(str,args)),exit=p.returncode,stdout=p.stdout,stderr=p.stderr,pass_=good);rows.append(row)
 if not good:fail.append(row);print('FAIL',row,flush=True)
 (out/'committed-matrix-results.json').write_text(json.dumps(report,indent=2)+'\n');return p
names=['numeric_lossless_widening','literal_local_read_at_two_widths','literal_local_takes_its_use_width','array_literal_takes_its_sibling_float_width','char_byte_literals','float_width_conversion','f64_tryop_widen','audit_numeric_types']
for name in names:
 case=root/'conformance/cases'/name;src=case/'main.fern';stdout=(case/'expected.stdout').read_text() if (case/'expected.stdout').exists() else '';code=int((case/'expected.exit').read_text()) if (case/'expected.exit').exists() else 0;formats=[]
 for tag,cli in [('Go',oracle),('self',selfcli)]:
  run(name+'-'+tag+'-check',[cli,'-check',src,root/'internal/stdlib'],stdout='')
  run(name+'-'+tag+'-interp',[cli,'-interp',src,root/'internal/stdlib'],stdout=stdout,exit=code)
  f=run(name+'-'+tag+'-fmt',[cli,'-fmt',src]);formats.append(f.stdout);formatted=work/(name+'-'+tag+'.fern');formatted.write_text(f.stdout)
  run(name+'-'+tag+'-fmt-again',[cli,'-fmt',formatted],stdout=f.stdout)
  run(name+'-'+tag+'-formatted-check',[cli,'-check',formatted,root/'internal/stdlib'],stdout='')
  run(name+'-'+tag+'-formatted-interp',[cli,'-interp',formatted,root/'internal/stdlib'],stdout=stdout,exit=code)
 if formats[0]!=formats[1]:fail.append(dict(name=name+'-formatter-parity'));print('FAIL formatter parity',name,flush=True)
 for target,runner in [('x86-64-linux',[]),('arm64-linux',['qemu-aarch64']),('wasm32-wasi',['wasmtime','run'])]:
  binary=work/(name+'-'+target);args=[selfcli,'-target',target,'-o',binary]
  if target=='wasm32-wasi':args+=['-emit','command-module']
  p=run(name+'-build-'+target,args+[src,root/'internal/stdlib'],stdout='')
  if p.returncode==0:run(name+'-run-'+target,runner+[binary],stdout=stdout,exit=code,census=True)
 print('DONE',name,flush=True)
for name in ['numeric_widening_rejects_narrowing','numeric_widening_rejects_stored_arrays','numeric_widening_float_remainder']:
 src=root/'conformance/cases'/name/'main.fern';err=(src.parent/'expected.error').read_text().strip()
 for tag,cli in [('Go',oracle),('self',selfcli)]:run(name+'-'+tag+'-check',[cli,'-check',src,root/'internal/stdlib'],error=err)
(out/'committed-matrix-results.json').write_text(json.dumps(report,indent=2)+'\n');assert not fail,len(fail);print('PASS',len(rows),'commands',flush=True)
