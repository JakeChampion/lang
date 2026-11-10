from pathlib import Path
import os,subprocess,json,hashlib,datetime
root=Path('/workspace/lang-interpreter-dispatch-20261109');fixed=Path('/workspace/lang-interpreter-main-base-20261109-2320');out=root/'build/interpreter-ci-20261110/reproduce';path=out/'qtrle-results.json';assert not path.exists()
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
source=fixed/'bench/video_qtrle.fern'
env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0')
d={'baseline_source':'4a04428264dd4641ca1032cf5edc84762a883dd5','candidate_code_source':'6f6bddfc2af38b6c7e85d23d9338a10c8fb9c307','pr_head':'7bd045eb04c8ea0b5980f0526e778ae107571d05','fixed_input_source':'4a04428264dd4641ca1032cf5edc84762a883dd5','entry_sha256':sha(source),'producer_source':'a19e87208c5931d2600fdb75cc966fd179ae8c8f','ci_run':42723690303,'ci_job':129524617582,'metric':'aarch64/video_qtrle.text','recorded_baseline':5682,'ci_measured':5600,'runs':[],'note':'Static emitted-assembly count, using the same whitespace-line rule as scripts/perf-bench. Not native execution or a timing sample. Both source pairs already include the main change that moved this output.'}
for label in ('baseline','candidate'):
 binary=root/f'build/interpreter-main-qualification-20261109-2320/{label}-normal';args=[str(binary),'-target','arm64-linux','-stdlib',str(fixed/'internal/stdlib'),str(source)]
 row={'label':label,'args':args,'cwd':str(fixed),'binary_sha256':sha(binary),'environment':{k:v for k,v in env.items() if k.startswith('FERN_')},'started':now()}
 asm=out/f'qtrle-{label}.s'
 with asm.open('wb') as stdout,(out/f'qtrle-{label}.stderr').open('wb') as stderr:r=subprocess.run(args,cwd=fixed,env=env,stdout=stdout,stderr=stderr,timeout=120)
 row.update(exit_code=r.returncode,finished=now(),asm_sha256=sha(asm));assert r.returncode==0
 count_args=['grep','-c','^[[:space:]]',str(asm)];count=subprocess.run(count_args,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,check=True);row.update(count_command=count_args,text_count=int(count.stdout.strip()));d['runs'].append(row);path.write_text(json.dumps(d,indent=2)+'\n');print(label,row['text_count'],row['asm_sha256'])
d['identical_assembly']=(out/'qtrle-baseline.s').read_bytes()==(out/'qtrle-candidate.s').read_bytes();d['finished']=now();path.write_text(json.dumps(d,indent=2)+'\n');assert d['identical_assembly'];assert all(r['text_count']==5600 for r in d['runs'])
