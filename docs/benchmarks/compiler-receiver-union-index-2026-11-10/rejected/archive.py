from pathlib import Path
import gzip,hashlib,json,subprocess,datetime
repo=Path('/workspace/lang-checker-union-index-20261110-1540');q=Path(__file__).resolve().parent;dst=repo/'docs/benchmarks/compiler-receiver-union-index-2026-11-10';sha=lambda b:hashlib.sha256(b).hexdigest();load=lambda p:json.loads(p.read_text())
assert not dst.exists(),'Never overwrite an evidence archive.'
for name in ['collector-result.json','prepare.json','normal-checks.json','validation.json','measurements/results.json','instructions/results.json']:
 r=load(q/name);assert r.get('finished')and r['exit_code']==0,name
m=load(q/'measurements/results.json');ir=load(q/'instructions/results.json');assert len(m['runs'])==84 and len(ir['runs'])==8
assert all(r['exit_code']==0 for r in m['runs']+ir['runs']);assert all(r['allocs']>0 and r['allocs']==r['frees']and r['live_bytes']==0 for r in m['runs']if r['counter_build'])
probes=[r for r in m['runs']if r['label'].endswith('-probe')];assert len(probes)==4 and all(r['cache_hits']>0 and r['cache_misses']==0 for r in probes)
assert all(r['exit_code']==r['expected_exit']for r in load(q/'normal-checks.json')['runs'])
oldq=repo/'build/qualification-main-20261110-1635';old=load(oldq/'measurements-v4/results.json');assert old['exit_code']==0 and len(old['runs'])==84
kernel=repo/'build/ci-kernel-failure-20261110-1647';fix=load(kernel/'fix-validation.json');assert fix['exit_code']==0 and all(r['exit_code']==0 for r in fix['runs']);assert load(kernel/'baseline-slow-timer-result.json')['exit_code']==1
assert not subprocess.check_output(['git','diff',m['candidate_source'],'HEAD','--','compiler','internal/stdlib','tests/stdlib','bootstrap/stage0.lock'],cwd=repo),'Current product differs from the measured source. Requalify or clearly archive this as historical.'
post={'date':datetime.datetime.now(datetime.timezone.utc).isoformat(),'sources':{},'binary_identities':{}}
for name,path,expected in [('baseline','/workspace/lang-compiler-main-20261110-1705',m['baseline_source']),('candidate','/workspace/lang-checker-union-candidate-20261110-1705',m['candidate_source']),('intermediate-baseline','/workspace/lang-compiler-main-20261110-1635',old['baseline_source']),('intermediate-candidate','/workspace/lang-checker-union-candidate-20261110-1635',old['candidate_source'])]:
 head=subprocess.check_output(['git','rev-parse','HEAD'],cwd=path,text=True).strip();status=subprocess.check_output(['git','status','--porcelain'],cwd=path,text=True);assert head==expected and not status;post['sources'][name]={'path':path,'head':head,'status':status}
for r in load(q/'prepare.json')['runs']:
 p=q/r['label'];assert sha(p.read_bytes())==r['binary_sha256'];post['binary_identities'][r['label']]={'sha256':r['binary_sha256'],'file_bytes':p.stat().st_size}
for name,identity in m['binary_identities'].items():
 assert sha(Path(identity['path']).read_bytes())==identity['sha256']
 post['binary_identities']['copied-'+name]=identity
producer=Path(load(q/'prepare.json')['runs'][0]['args'][0]);assert sha(producer.read_bytes())==m['producer_sha256']
assert 'x86-64-linux '+m['producer_sha256'] in Path('/workspace/lang-compiler-main-20261110-1705/bootstrap/stage0.lock').read_text()
for w in ['opus','checker']:
 for folder in ['measurements','instructions']:
  p=q/folder/(w+'-reference');assert sha(p.read_bytes())==m['outputs'][w];post['binary_identities'][folder+'-'+w]={'sha256':sha(p.read_bytes()),'file_bytes':p.stat().st_size}
 assert len({r['output_sha256']for r in ir['runs']if r['workload']==w})==1
 assert all(r['output_sha256']==m['outputs'][w]for r in ir['runs']if r['workload']==w)
 assert m['program_controls'][w]['exit_code']==0
assert m['program_controls']['opus']['stdout_sha256']=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
(q/'post-run-source-audit.json').write_text(json.dumps(post,indent=2)+'\n')
dst.mkdir();compressed={};sources={}
def retain(p,target):
 raw=p.read_bytes();t=dst/target;t.parent.mkdir(parents=True,exist_ok=True)
 compress=p.suffix in ['.callgrind','.jsonl','.s','.log','.patch','.go','.md']or len(raw)>262144 or any(line.endswith((b' ',b'\t',b'\r'))for line in raw.split(b'\n'))
 if compress:
  t=t.with_name(t.name+'.gz');t.write_bytes(gzip.compress(raw,mtime=0));compressed[str(t.relative_to(dst))]={'original_sha256':sha(raw),'original_bytes':len(raw)}
 else:t.write_bytes(raw)
 sources[str(t.relative_to(dst))]={'original_path':str(p),'original_sha256':sha(raw),'original_bytes':len(raw),'compressed':compress}
exts={'.py','.sh','.json','.jsonl','.stdout','.stderr','.log','.callgrind','.s','.txt','.go','.md'}
for label,source in [('current',q),('intermediate',oldq),('initial',repo/'build/qualification-20261110-1600'),('initial-baseline-profile',repo/'build/baseline-profile-20261110-1540'),('kernel-control',kernel)]:
 for p in sorted(source.rglob('*')):
  if not p.is_file()or'__pycache__'in p.parts:continue
  rel=p.relative_to(source)
  if any('-memo-'in part or part.startswith('memo')for part in rel.parts):continue
  if p.suffix in exts:retain(p,Path(label)/rel)
for label,folder,names in [('initial-resume','resume-20261110-1625',['resume-instructions.py','resume-instructions.log']),('integration','main-integration-20261110-1632',['preflight.json','merge.log','freeze.py','freeze.json','storage-failures.json','push-preflight.json','push-result.json','push.stdout','push.stderr']),('latest-integration','main-integration-20261110-1705',['preflight.json','merge.log','freeze.py','freeze.json','source-freeze.json','main-before-qualification.json','push-preflight.json','push-result.json','push.stdout','push.stderr'])]:
 for name in names:
  p=repo/'build'/folder/name
  if p.exists():retain(p,Path(label)/name)
# Preserve the earlier rejected main-head preflight, without private automation metadata.
p=repo/'build/main-integration-20261110-1630/preflight.json'
if p.exists():retain(p,'initial-main-race/preflight.json')
for name in ['install.py','result.json','version.stdout','version.stderr','download.stdout','download.stderr','extract.log']:
 p=Path('/workspace/scratch/lang-qemu-system-20261110-1650')/name
 if p.exists():retain(p,Path('qemu-toolchain')/name)
patch=subprocess.check_output(['git','diff',m['baseline_source'],m['candidate_source'],'--','compiler','internal/testing/e2ecompiler'],cwd=repo);(q/'implementation.patch').write_bytes(patch);retain(q/'implementation.patch','implementation.patch')
retain(q/'verify.py','verify.py')
for rel in ['compiler/checker.fern','tests/stdlib/opus_delimited_test.fern','bootstrap/stage0.lock']:
 retain(Path('/workspace/lang-compiler-main-20261110-1705')/rel,Path('fixed-inputs')/(rel+'.txt'))
summary={'current':{'baseline_source':m['baseline_source'],'candidate_source':m['candidate_source'],'timings':m['summary'],'allocation_controls':[r for r in m['runs']if r['counter_build']],'instruction_controls':ir['runs'],'outputs':m['outputs'],'program_controls':m['program_controls'],'cache_probes':probes},'intermediate':{'baseline_source':old['baseline_source'],'candidate_source':old['candidate_source'],'timings':old['summary'],'allocation_controls':[r for r in old['runs']if r['counter_build']],'outputs':old['outputs'],'program_controls':old['program_controls'],'limitations':load(oldq/'concurrency-note.json')},'initial_matched_instructions':load(repo/'build/qualification-20261110-1600/opus-cold-instructions-resume/results.json'),'kernel':{'ordinary_main_six':load(kernel/'reproduction.json'),'negative_slow_timer':load(kernel/'baseline-slow-timer-result.json'),'fixed':fix},'limits':'Each source comparison remains separate. No pooled reductions, external rank, general compiler timing gain or normal full-run leak claim.'}
(dst/'summary.json').write_text(json.dumps(summary,indent=2)+'\n');(dst/'compression-identities.json').write_text(json.dumps(compressed,indent=2)+'\n');(dst/'source-files.json').write_text(json.dumps(sources,indent=2)+'\n')
(dst/'manifest.json').write_text(json.dumps({'sha256':{str(p.relative_to(dst)):sha(p.read_bytes())for p in sorted(dst.rglob('*'))if p.is_file()}},indent=2)+'\n');print(json.dumps({'archive':str(dst),'data_hashes':len(load(dst/'manifest.json')['sha256']),'raw_identities':len(compressed)}))
