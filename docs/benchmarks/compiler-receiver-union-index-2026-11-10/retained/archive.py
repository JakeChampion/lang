from pathlib import Path
import datetime,gzip,hashlib,json,subprocess
repo=Path('/workspace/lang-checker-union-index-20261110-1540');out=Path(__file__).resolve().parent;rejected=repo/'build/qualification-main-20261110-1705';intermediate=repo/'build/qualification-main-20261110-1635';initial=repo/'build/qualification-20261110-1600';kernel=repo/'build/ci-kernel-failure-20261110-1647';dst=repo/'docs/benchmarks/compiler-receiver-union-index-2026-11-10';load=lambda p:json.loads(p.read_text());sha=lambda b:hashlib.sha256(b).hexdigest()
assert not dst.exists(),'Never overwrite an archive.'
for name in ['collector-result.json','build.json','validation.json','unit-controls.json','normal-own-source.json','native-opus.json']:
 r=load(out/name);assert r.get('finished')and r['exit_code']==0,name
m=load(rejected/'measurements/results.json');old=load(intermediate/'measurements-v4/results.json');decision=load(rejected/'decision.json');ir=load(rejected/'instructions/results.json');assert decision['decision'].startswith('REJECT');assert ir['exit_code']==1 and len(ir['runs'])==1 and ir['runs'][0]['exit_code']==-15
for matrix in [m,old]:
 assert matrix['exit_code']==0 and len(matrix['runs'])==84
 assert all(r['exit_code']==0 for r in matrix['runs'])
 assert all(r['allocs']>0 and r['allocs']==r['frees']and r['live_bytes']==0 for r in matrix['runs']if r['counter_build'])
 assert all(r['cache_misses']==0 and r['cache_hits']>0 for r in matrix['runs']if r['label'].endswith('-probe'))
 assert all(matrix['program_controls'][w]['exit_code']==0 for w in ['opus','checker'])
 assert matrix['program_controls']['opus']['stdout_sha256']=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
retained=load(out/'source-freeze.json');assert not subprocess.check_output(['git','diff',retained['baseline'],retained['retained'],'--','compiler','internal/stdlib'],cwd=repo)
assert not subprocess.check_output(['git','diff',retained['retained'],'HEAD','--','compiler','internal/stdlib','internal/testing/e2ecompiler','bootstrap/stage0.lock'],cwd=repo),'Retained code differs from validated source.'
fix=load(kernel/'fix-validation.json');assert fix['exit_code']==0 and all(r['exit_code']==0 for r in fix['runs']);assert load(kernel/'baseline-slow-timer-result.json')['exit_code']==1
build=load(out/'build.json');assert build['runs'][0]['sha256']==build['runs'][1]['sha256']==build['identical_normal_sha256'];assert all(r['exit_code']==0 for r in build['runs'])
post={'date':datetime.datetime.now(datetime.timezone.utc).isoformat(),'sources':{},'binary_identities':{},'scope':'Final compiler and standard library equal main. All performance comparisons concern rejected historical candidates.'}
for label,path,head in [('main','/workspace/lang-compiler-main-20261110-1750',retained['baseline']),('retained','/workspace/lang-checker-union-retained-20261110-1750',retained['retained']),('rejected-baseline','/workspace/lang-compiler-main-20261110-1705',m['baseline_source']),('rejected-candidate','/workspace/lang-checker-union-candidate-20261110-1705',m['candidate_source'])]:
 actual=subprocess.check_output(['git','rev-parse','HEAD'],cwd=path,text=True).strip();status=subprocess.check_output(['git','status','--porcelain'],cwd=path,text=True);assert actual==head and not status;post['sources'][label]={'path':path,'head':actual,'status':status}
for label in ['main','retained']:
 p=out/(label+'-normal');assert sha(p.read_bytes())==build['identical_normal_sha256'];post['binary_identities'][label]={'sha256':sha(p.read_bytes()),'file_bytes':p.stat().st_size}
for p in [out/'gofmt.stdout',out/'format-check.stdout']:assert not p.read_bytes()
assert load(out/'native-opus.json')['tap_sha256']=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
(out/'post-run-audit.json').write_text(json.dumps(post,indent=2)+'\n');dst.mkdir();compressed={};sources={}
def retain(p,target):
 raw=p.read_bytes();t=dst/target;t.parent.mkdir(parents=True,exist_ok=True)
 zipped=p.suffix in ['.callgrind','.jsonl','.s','.log','.patch','.go','.md']or len(raw)>262144 or any(line.endswith((b' ',b'\t',b'\r'))for line in raw.split(b'\n'))
 if zipped:
  t=t.with_name(t.name+'.gz');t.write_bytes(gzip.compress(raw,mtime=0));compressed[str(t.relative_to(dst))]={'original_sha256':sha(raw),'original_bytes':len(raw)}
 else:t.write_bytes(raw)
 sources[str(t.relative_to(dst))]={'original_path':str(p),'original_sha256':sha(raw),'original_bytes':len(raw),'compressed':zipped}
exts={'.py','.sh','.json','.jsonl','.stdout','.stderr','.log','.callgrind','.s','.txt','.go','.md','.patch'}
for label,folder in [('retained',out),('rejected',rejected),('intermediate',intermediate),('initial',initial),('initial-baseline-profile',repo/'build/baseline-profile-20261110-1540'),('kernel-control',kernel)]:
 for p in sorted(folder.rglob('*')):
  if not p.is_file()or'__pycache__'in p.parts:continue
  rel=p.relative_to(folder)
  if any('-memo-'in part or part.startswith('memo')for part in rel.parts):continue
  if any('automation'in part or'checkpoint'in part for part in rel.parts):continue
  if p.suffix in exts:retain(p,Path(label)/rel)
for label,folder,names in [('initial-resume','resume-20261110-1625',['resume-instructions.py','resume-instructions.log']),('integration1632','main-integration-20261110-1632',['preflight.json','merge.log','freeze.py','freeze.json','storage-failures.json','push-preflight.json','push-result.json','push.stdout','push.stderr']),('integration1705','main-integration-20261110-1705',['preflight.json','merge.log','freeze.py','freeze.json','source-freeze.json','main-before-qualification.json','push-preflight.json','push-result.json','push.stdout','push.stderr'])]:
 for name in names:
  p=repo/'build'/folder/name
  if p.exists():retain(p,Path(label)/name)
for name in ['install.py','result.json','version.stdout','version.stderr','download.stdout','download.stderr','extract.log']:
 p=Path('/workspace/scratch/lang-qemu-system-20261110-1650')/name
 if p.exists():retain(p,Path('qemu-toolchain')/name)
for label,base,head in [('rejected-trial',m['baseline_source'],m['candidate_source']),('retained',retained['baseline'],retained['retained'])]:
 patch=subprocess.check_output(['git','diff',base,head,'--','compiler','internal/testing/e2ecompiler'],cwd=repo);p=out/(label+'.patch');p.write_bytes(patch);retain(p,label+'.patch')
retain(out/'verify.py','verify.py')
summary={'decision':decision,'rejected':{'baseline_source':m['baseline_source'],'candidate_source':m['candidate_source'],'timings':m['summary'],'allocation_controls':[r for r in m['runs']if r['counter_build']],'outputs':m['outputs'],'program_controls':m['program_controls'],'cache_probes':[r for r in m['runs']if r['label'].endswith('-probe')],'partial_instruction_control':ir},'intermediate':{'baseline_source':old['baseline_source'],'candidate_source':old['candidate_source'],'timings':old['summary'],'allocation_controls':[r for r in old['runs']if r['counter_build']],'outputs':old['outputs'],'limitations':load(intermediate/'concurrency-note.json')},'initial_instructions':load(initial/'opus-cold-instructions-resume/results.json'),'kernel':{'ordinary_main_six':load(kernel/'reproduction.json'),'negative_slow_timer':load(kernel/'baseline-slow-timer-result.json'),'fixed':fix},'retained':{'source':retained,'build':build,'validation':load(out/'validation.json'),'units':load(out/'unit-controls.json'),'normal_own_source':load(out/'normal-own-source.json'),'full_correctness':load(out/'collector-result.json'),'native_opus':load(out/'native-opus.json')},'limits':'No compiler optimization remains. No source totals are pooled. The interrupted trace supplies no completed instruction comparison. No retained speedup, external rank, timing cause, or normal-run leak claim.'}
for name,data in [('summary.json',summary),('compression-identities.json',compressed),('source-files.json',sources)]:
 (dst/name).write_text(json.dumps(data,indent=2)+'\n')
(dst/'manifest.json').write_text(json.dumps({'sha256':{str(p.relative_to(dst)):sha(p.read_bytes())for p in sorted(dst.rglob('*'))if p.is_file()}},indent=2)+'\n');print(json.dumps({'archive':str(dst),'data_hashes':len(load(dst/'manifest.json')['sha256']),'raw_gzip_identities':len(compressed)}))
