from pathlib import Path
import subprocess,json,datetime,time,shutil
q=Path(__file__).resolve().parent;state={'purpose':'Free regenerable Go object cache outside all repositories. Preserve every worktree, compiler executable, ignored measurement archive, toolchain download and raw failure. Wait until Go validation and CI self-tests finish before cache cleanup.','runs':[]}
def save():(q/'storage-cache.json').write_text(json.dumps(state,indent=2)+'\n')
save()
try:
 deadline=time.monotonic()+3600
 while True:
  r=json.loads((q/'collector-result-v2.json').read_text());assert r.get('exit_code')in[None,0]
  if any(x['label']=='normal-full-correctness'for x in r['runs']):break
  assert time.monotonic()<deadline;time.sleep(2)
 path=subprocess.check_output(['go','env','GOCACHE'],text=True).strip();assert path=='/home/agent/.cache/go-build';state['cache_path']=path;state['disk_before']=shutil.disk_usage('/workspace')._asdict();state['started']=datetime.datetime.now(datetime.timezone.utc).isoformat();save()
 with(q/'storage-cache.stdout').open('wb')as so,(q/'storage-cache.stderr').open('wb')as se:r=subprocess.run(['go','clean','-cache'],stdout=so,stderr=se)
 state.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat(),disk_after=shutil.disk_usage('/workspace')._asdict());save();assert r.returncode==0
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
