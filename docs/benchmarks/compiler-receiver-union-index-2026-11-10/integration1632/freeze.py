from pathlib import Path
import subprocess,os,hashlib,json
repo=Path('/workspace/lang-checker-union-index-20261110-1540');old=Path('/workspace/lang-compiler-main-20261110-1540');out=Path(__file__).parent
assert not subprocess.check_output(['git','status','--porcelain'],cwd=old,text=True)
def tree(sha):
 rows=subprocess.check_output(['git','ls-tree','-rz','--full-tree',sha],cwd=repo).split(b'\0');d={}
 for row in rows:
  if row:
   meta,path=row.split(b'\t',1);mode,typ,blob=meta.decode().split();d[os.fsdecode(path)]=(mode,blob)
 return d
previous=tree('0e715dffcb9220479b3b6b5dc2055c81cd990497');reports=[]
for dest,head in [('/workspace/lang-compiler-main-20261110-1635','515ddee8d0c06db2b94b4f3a86f3bf1c59cd2909'),('/workspace/lang-checker-union-candidate-20261110-1635','3d51e1880abc5025731f23772f1a343fe9c7fcbf')]:
 target=Path(dest);assert not target.exists()
 subprocess.run(['git','worktree','add','--detach','--no-checkout',dest,head],cwd=repo,check=True,capture_output=True)
 linked=written=0
 for name,(mode,blob)in tree(head).items():
  f=target/name;f.parent.mkdir(parents=True,exist_ok=True)
  if mode=='120000':
   data=subprocess.check_output(['git','cat-file','blob',blob],cwd=repo);f.symlink_to(os.fsdecode(data));written+=1
  elif previous.get(name)==(mode,blob):os.link(old/name,f);linked+=1
  else:
   f.write_bytes(subprocess.check_output(['git','cat-file','blob',blob],cwd=repo));f.chmod(0o755 if mode=='100755'else 0o644);written+=1
 subprocess.run(['git','read-tree','HEAD'],cwd=target,check=True)
 assert not subprocess.check_output(['git','status','--porcelain'],cwd=target,text=True)
 reports.append({'path':dest,'head':head,'unchanged_files_hardlinked':linked,'separate_files':written,'note':'Immutable source snapshot. Shared source files must never be edited. Existing frozen source files remain untouched.'})
(out/'freeze.json').write_text(json.dumps(reports,indent=2)+'\n');print(reports)
