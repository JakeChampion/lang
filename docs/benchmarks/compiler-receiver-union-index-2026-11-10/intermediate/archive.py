from pathlib import Path
import datetime,gzip,hashlib,json,subprocess,os
repo=Path('/workspace/lang-checker-union-index-20261110-1540');q=Path(__file__).resolve().parent;dst=repo/'docs/benchmarks/compiler-receiver-union-index-2026-11-10';sha=lambda b:hashlib.sha256(b).hexdigest();load=lambda p:json.loads(p.read_text())
assert not dst.exists(),'Never overwrite an archive.'
for path in [q/'collector-result-v2.json',q/'prepare.json',q/'validation-tmpfs/validation.json',q/'measurements-v4/results.json',q/'instructions-v3/results.json']:
 r=load(path);assert r.get('finished')and r['exit_code']==0,str(path)
measure=load(q/'measurements-v4/results.json');ir=load(q/'instructions-v3/results.json');assert len(ir['runs'])==8 and len(measure['runs'])==84
assert all(r['exit_code']==0 for r in measure['runs']+ir['runs']);assert all(r['allocs']==r['frees']and r['live_bytes']==0 for r in measure['runs']if r['counter_build'])
assert all(r['cache_misses']==0 and r['cache_hits']>0 for r in measure['runs']if r['label'].endswith('-probe'))
assert {r['label']:r for r in load(q/'collector-result-v2.json')['runs']}['normal-full-correctness']['stdout_sha256']=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
old=repo/'build/qualification-20261110-1600';assert load(old/'opus-cold-instructions-resume/results.json')['exit_code']==0
sourceaudit={}
for label,path,expected in [('baseline','/workspace/lang-compiler-main-20261110-1635',measure['baseline_source']),('candidate','/workspace/lang-checker-union-candidate-20261110-1635',measure['candidate_source']),('historical-baseline','/workspace/lang-compiler-main-20261110-1540','0e715dffcb9220479b3b6b5dc2055c81cd990497'),('historical-candidate','/workspace/lang-checker-union-candidate-20261110-1600','99fda7a649924cdc341f014f52e1d4ab9a6ad0f4')]:
 head=subprocess.check_output(['git','rev-parse','HEAD'],cwd=path,text=True).strip();status=subprocess.check_output(['git','status','--porcelain'],cwd=path,text=True);assert head==expected and not status;sourceaudit[label]={'path':path,'head':head,'status':status}
for row in load(q/'prepare.json')['runs']:assert sha((q/row['label']).read_bytes())==row['binary_sha256']
dst.mkdir();compressed={};excluded=[]
def retain(p,target):
 raw=p.read_bytes();t=dst/target;t.parent.mkdir(parents=True,exist_ok=True)
 if p.suffix in ['.callgrind','.jsonl','.s','.log','.patch']or len(raw)>262144 or any(x.endswith((b' ',b'\t',b'\r'))for x in raw.split(b'\n')):
  t=t.with_name(t.name+'.gz');t.write_bytes(gzip.compress(raw,mtime=0));compressed[str(t.relative_to(dst))]={'original_sha256':sha(raw),'original_bytes':len(raw),'original_path':str(p)}
 else:t.write_bytes(raw)
exts={'.py','.sh','.json','.jsonl','.stdout','.stderr','.log','.callgrind','.s','.txt'}
for label,source in [('current',q),('initial',old),('initial-baseline-profile',repo/'build/baseline-profile-20261110-1540'),('resume',repo/'build/resume-20261110-1625'),('main-integration',repo/'build/main-integration-20261110-1632')]:
 for path in sorted(source.rglob('*')):
  if not path.is_file()or '__pycache__'in path.parts:continue
  if any(part.startswith('memo')or'-memo-'in part for part in path.relative_to(source).parts):continue
  if path.suffix in exts:retain(path,Path(label)/path.relative_to(source))
retain(q/'verify.py','verify.py')
patch=subprocess.check_output(['git','diff',measure['baseline_source'],measure['candidate_source'],'--','compiler','internal/testing/e2ecompiler'],cwd=repo);p=q/'implementation.patch';p.write_bytes(patch);retain(p,'implementation.patch')
summary={'source_audit':sourceaudit,'measurements':measure['summary'],'allocation_controls':[r for r in measure['runs']if r['counter_build']],'instruction_controls':ir['runs'],'native_outputs':measure['outputs'],'program_controls':measure['program_controls'],'source_scope_note':'Initial0e/99fd counts remain separate. Current515d/3d51 data do not pool older totals. No world rank. All original collector failures and pre-execution stops are retained.'}
(dst/'summary.json').write_text(json.dumps(summary,indent=2)+'\n');(dst/'compression-identities.json').write_text(json.dumps(compressed,indent=2)+'\n')
(dst/'manifest.json').write_text(json.dumps({'sha256':{str(p.relative_to(dst)):sha(p.read_bytes())for p in sorted(dst.rglob('*'))if p.is_file()}},indent=2)+'\n');print('hashes',len(load(dst/'manifest.json')['sha256']),'raw-identities',len(compressed))
