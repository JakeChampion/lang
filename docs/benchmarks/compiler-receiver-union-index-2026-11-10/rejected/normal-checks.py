from pathlib import Path
import json,subprocess,os,datetime,hashlib
out=Path(__file__).resolve().parent;fixed=Path('/workspace/lang-compiler-main-20261110-1705');candidate=Path('/workspace/lang-checker-union-candidate-20261110-1705');state={'baseline':'b442b6b93ad733a8e71e972fd0ee31620ee497a3','candidate':'67b216378af4117cde0de6dc92a373a7636cccd2','purpose':'Both fresh normal compilers check their own sources and preserve main E098 refusals. Negative cases expect exit1, not a successful compilation.','runs':[]}
def save():(out/'normal-checks.json').write_text(json.dumps(state,indent=2)+'\n')
save()
try:
 env={k:v for k,v in os.environ.items()if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0')
 for name,source in [('baseline',fixed),('candidate',candidate)]:
  args=[str(out/(name+'-normal')),'-check','compiler/fern.fern','internal/stdlib'];label=name+'-own-source';row={'label':label,'args':args,'cwd':str(source),'expected_exit':0,'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save()
  with(out/(label+'.stdout')).open('wb')as so,(out/(label+'.stderr')).open('wb')as se:r=subprocess.run(args,cwd=source,env=env,stdout=so,stderr=se,timeout=1980)
  row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0
 cases=sorted((fixed/'conformance/cases').glob('diag_e098_intrinsic_as_value_*'));assert len(cases)==5
 for case in cases:
  outputs=[]
  for name in ['baseline','candidate']:
   label=case.name+'-'+name;args=[str(out/(name+'-normal')),'-check',str(case/'main.fern'),str(fixed/'internal/stdlib')];row={'label':label,'args':args,'expected_exit':1,'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save()
   r=subprocess.run(args,cwd=fixed,env=env,capture_output=True,timeout=1980);(out/(label+'.stdout')).write_bytes(r.stdout);(out/(label+'.stderr')).write_bytes(r.stderr);row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat(),stdout_sha256=hashlib.sha256(r.stdout).hexdigest(),stderr_sha256=hashlib.sha256(r.stderr).hexdigest());save();assert r.returncode==1 and b'E098'in r.stderr and(case/'expected.error').read_text().strip().encode()in r.stderr;outputs.append((r.stdout,r.stderr))
  assert outputs[0]==outputs[1]
 state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
