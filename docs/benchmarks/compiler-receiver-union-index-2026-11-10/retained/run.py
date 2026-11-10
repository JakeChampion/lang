from pathlib import Path
import json,subprocess,datetime,os,time,hashlib
out=Path(__file__).resolve().parent
state={'baseline':'e4494347c2f16a96727563c4495bd9826fdfadd0','candidate':'c02d2b4c1b2e3232da1dc4f7575383a65c3d9eaa','runs':[]}
def save():(out/'collector-result.json').write_text(json.dumps(state,indent=2)+'\n')
save()
try:
 for file in ['quick.py','build.py','validate.py']:

  row={'label':file,'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save()
  with(out/(file+'.log')).open('wb')as log:r=subprocess.run(['python3',str(out/file)],stdout=log,stderr=subprocess.STDOUT)
  row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0
 row={'label':'ci-selftest','started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save()
 with(out/'ci-selftest.stdout').open('wb')as so,(out/'ci-selftest.stderr').open('wb')as se:r=subprocess.run(['make','ci-selftest'],cwd='/workspace/lang-checker-union-retained-20261110-1750',stdout=so,stderr=se)
 row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0
 env={k:v for k,v in os.environ.items()if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0');fixed=Path('/workspace/lang-compiler-main-20261110-1750')
 args=[str(out/'retained-normal'),'-stdlib',str(fixed/'internal/stdlib'),'-interp',str(fixed/'tests/stdlib/opus_delimited_test.fern')];row={'label':'normal-full-correctness','args':args,'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'note':'No paired timing or normal-run leak claim. Original1980s diagnostic budget.'};state['runs'].append(row);save()
 with(out/'full.stdout').open('wb')as so,(out/'full.stderr').open('wb')as se:r=subprocess.run(args,cwd=fixed,env=env,stdout=so,stderr=se,timeout=1980)
 row.update(exit_code=r.returncode,stdout_sha256=hashlib.sha256((out/'full.stdout').read_bytes()).hexdigest(),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0 and row['stdout_sha256']=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
 state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
