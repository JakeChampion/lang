from pathlib import Path
import subprocess,os,hashlib,json,datetime
out=Path(__file__).resolve().parent;fixed=Path('/workspace/lang-compiler-main-20261110-1845');b=out/'retained-normal';build=json.loads((out/'build.json').read_text());sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest();state={'source':'63d33384bb4261faa38f4436b4abf4d52a9b9542','fixed_input_source':'bcd223bae6ab2d8dc14b3b152027fa7be90e6779','compiler_sha256':sha(b),'purpose':'One current-main-equivalent native Opus correctness control after upstream parser and mismatch-kernel changes. Not a paired timing or normal-run leak check.','runs':[]}
def save():(out/'native-opus.json').write_text(json.dumps(state,indent=2)+'\n')
save()
try:
 assert build['exit_code']==0 and sha(b)==build['identical_normal_sha256'];env={k:v for k,v in os.environ.items()if not k.startswith('FERN_')};env.update(FERN_PROFILE='0',FERN_LEAKCHECK='0',FERN_RC_CERTIFY='1',FERN_RC_CERTIFY_MEMO='0')
 for label,args in [('compile',[str(b),'-target','x86-64-linux','-o',str(out/'opus-native'),str(fixed/'tests/stdlib/opus_delimited_test.fern'),str(fixed/'internal/stdlib')]),('program',[str(out/'opus-native')])]:
  row={'label':label,'args':args,'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save()
  with(out/('native-opus-'+label+'.stdout')).open('wb')as so,(out/('native-opus-'+label+'.stderr')).open('wb')as se:r=subprocess.run(args,cwd=fixed,env=env,stdout=so,stderr=se,timeout=1980)
  row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0
  if label=='program':assert sha(out/'native-opus-program.stdout')=='07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161'
 state.update(exit_code=0,native_sha256=sha(out/'opus-native'),tap_sha256=sha(out/'native-opus-program.stdout'),finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
