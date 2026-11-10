from pathlib import Path
import os,subprocess,json,datetime
p=Path('build/interpreter-index-trial-20261109');data=[];f=p/'verification.json';assert not f.exists()
for name,args in [('vet',['go','vet','./internal/testing/e2ecompiler']),('ci-selftest',['make','ci-selftest'])]:
 d={'name':name,'args':args,'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};data.append(d);f.write_text(json.dumps(data,indent=2)+'\n')
 with (p/(name+'.log')).open('wb') as out:r=subprocess.run(args,stdout=out,stderr=subprocess.STDOUT)
 d.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());f.write_text(json.dumps(data,indent=2)+'\n');print(name,r.returncode,flush=True)
