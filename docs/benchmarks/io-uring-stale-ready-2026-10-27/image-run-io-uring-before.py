from pathlib import Path
import subprocess,json,os,hashlib,re,time
root=Path('/workspace/lang-image-io-uring-syscalls');prefix=Path('/workspace/image-io-uring-current-before');assert not prefix.with_suffix('.json').exists()
files=subprocess.check_output(['git','ls-files','compiler','internal/stdlib','internal/testing/e2ecompiler/self_host_vdso_clock_ptrace_test.go','internal/testing/e2eharness/serve_allocs.go'],cwd=root,text=True).splitlines()
def inventory():return {p:hashlib.sha256((root/p).read_bytes()).hexdigest()for p in files if (root/p).is_file()}
assert not subprocess.check_output(['git','status','--porcelain'],cwd=root)
before=inventory();cmd=['go','test','-v','./internal/testing/e2ecompiler','-run','^TestSelfHostServeSyscallsPerRequest(TwoFetch)?$','-count=1','-timeout=10m'];start=time.monotonic()
with prefix.with_suffix('.log').open('w')as log:r=subprocess.run(cmd,cwd=root,env=dict(os.environ,FERN_IO_URING='1'),stdout=log,stderr=subprocess.STDOUT)
s=prefix.with_suffix('.log').read_text();record=dict(source_commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),source_hashes=before,sources_unchanged=before==inventory(),command=cmd,FERN_IO_URING='1',exit_code=r.returncode,seconds=time.monotonic()-start,PASS_nodes=len(re.findall(r'^\s*--- PASS:',s,re.M)),FAIL_nodes=len(re.findall(r'^\s*--- FAIL:',s,re.M)),SKIP_nodes=len(re.findall(r'^\s*--- SKIP:',s,re.M)))
prefix.with_suffix('.json').write_text(json.dumps(record,indent=2)+'\n');print({k:v for k,v in record.items()if k!='source_hashes'})
raise SystemExit(r.returncode)
