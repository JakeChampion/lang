from pathlib import Path
import subprocess,json,time,hashlib
root=Path('/workspace/lang-image-io-uring-syscalls');prefix=Path('/workspace/image-io-uring-clean-build');assert not prefix.with_suffix('.json').exists()
assert not subprocess.check_output(['git','status','--porcelain'],cwd=root)
files=subprocess.check_output(['git','ls-files','compiler','internal/stdlib'],cwd=root,text=True).splitlines()
def inventory():return {p:hashlib.sha256((root/p).read_bytes()).hexdigest()for p in files if(root/p).is_file()}
before=inventory();source=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip();start=time.monotonic()
with prefix.with_suffix('.log').open('w')as log:
 r=subprocess.run(['make','bin/fern'],cwd=root,stdout=log,stderr=subprocess.STDOUT)
 v=subprocess.run(['bin/fern','-version'],cwd=root,stdout=log,stderr=subprocess.STDOUT)if r.returncode==0 else None
record=dict(source_commit=source,make_exit=r.returncode,version_exit=v.returncode if v else None,seconds=time.monotonic()-start,source_hashes=before,sources_unchanged=before==inventory(),binaries={p:hashlib.sha256((root/p).read_bytes()).hexdigest()for p in ['bin/fern','bin/fern-selfhost']if(root/p).is_file()})
prefix.with_suffix('.json').write_text(json.dumps(record,indent=2)+'\n');print({k:v for k,v in record.items()if k!='source_hashes'})
assert r.returncode==0 and v.returncode==0 and record['sources_unchanged']
