import hashlib, json, subprocess, time
from pathlib import Path
root=Path('/tmp/image-engine-worktrees/lang-image-arm64-large-offset-health')
prefix=Path('/tmp/image-arm64-large-offset-unique-label-build')
def git(*args): return subprocess.check_output(['git',*args],cwd=root,text=True).strip()
assert git('rev-parse','HEAD')==Path('/tmp/image-arm64-large-offset-unique-label-source-sha.txt').read_text().strip()
assert not git('status','--porcelain')
paths=subprocess.check_output(['git','ls-files','compiler','internal/stdlib'],cwd=root,text=True).splitlines()
def hashes():return {p:hashlib.sha256((root/p).read_bytes()).hexdigest()for p in paths if(root/p).is_file()}
before=hashes();started=time.time()
with prefix.with_suffix('.log').open('w')as log:
    made=subprocess.run(['make','bin/fern'],cwd=root,stdout=log,stderr=subprocess.STDOUT)
    log.write('\nACTUAL_MAKE_EXIT='+str(made.returncode)+'\n');log.flush()
    version=subprocess.run([str(root/'bin/fern'),'-version'],cwd=root,capture_output=True,text=True)if made.returncode==0 else None
    if version:log.write(version.stdout+version.stderr+'\nACTUAL_VERSION_EXIT='+str(version.returncode)+'\n')
record=dict(source=git('rev-parse','HEAD'),make_exit=made.returncode,version_exit=None if version is None else version.returncode,
            version=None if version is None else version.stdout.strip(),seconds=time.time()-started,sources_unchanged=before==hashes(),source_hashes=before,
            binaries={p:hashlib.sha256((root/p).read_bytes()).hexdigest()for p in['bin/fern','bin/fern-selfhost']if(root/p).is_file()},worktree_clean=not git('status','--porcelain'))
prefix.with_suffix('.json').write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps({k:v for k,v in record.items()if k!='source_hashes'}))
assert record['make_exit']==record['version_exit']==0 and record['sources_unchanged']and record['worktree_clean']
