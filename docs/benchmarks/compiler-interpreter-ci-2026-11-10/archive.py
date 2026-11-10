from pathlib import Path
import json,hashlib,gzip,shutil,datetime
root=Path('/workspace/lang-interpreter-dispatch-20261109');local=root/'build/interpreter-ci-20261110';out=root/'docs/benchmarks/compiler-interpreter-ci-2026-11-10';assert not out.exists();out.mkdir(parents=True)
def sha(b):return hashlib.sha256(b).hexdigest()
records=[]
def put(src,dest,compressed=False):
 data=src.read_bytes();target=out/dest;target.parent.mkdir(parents=True,exist_ok=True);assert not target.exists();stored=gzip.compress(data,compresslevel=9,mtime=0) if compressed else data;target.write_bytes(stored);records.append({'path':dest,'original_sha256':sha(data),'original_bytes':len(data),'gzip':compressed})
for name in ('android-stopwatch-129524621282.log','perf-aarch64-129524617582.log','perf-gate-selftest.log','qtrle-gate.log'):
 put(local/name,'ci/'+name+'.gz',True)
put(local/'qtrle-row.txt','ci/qtrle-row.txt')
for p in sorted((local/'reproduce').iterdir()):
 if not p.is_file():continue
 comp=p.suffix in ('.s','.stdout','.stderr');put(p,'reproduce/'+p.name+('.gz' if comp else ''),comp)
for num in (0,3):
 src=root/f'build/interpreter-shard{num}-20261110'
 for name in ('run.sh','result.json','tests.jsonl','tests.stderr','selection-correction.json'):
  p=src/name
  if p.exists():
   comp=p.suffix in ('.jsonl','.stderr');put(p,f'shard{num}/'+name+('.gz' if comp else ''),comp)
src=root/'build/interpreter-source-gate-20261110'
for name in ('run.sh','result.json','check-sources.log'):
 comp=name.endswith('.log');put(src/name,'source-gate/'+name+('.gz' if comp else ''),comp)
src=root/'build/interpreter-index-profile-20261110'
for p in sorted(src.iterdir()):
 if not p.is_file() or p.name in ('fern-cpu','data-sha256.json'):continue
 comp=p.suffix in ('.stdout','.stderr','.log');put(p,'profile/'+p.name+('.gz' if comp else ''),comp)
put(root/'build/interpreter-main-qualification-20261109-2320/identities.json','comparison-identities.json') if (root/'build/interpreter-main-qualification-20261109-2320/identities.json').exists() else None
put(root/'bench/video_qtrle.fern','input/video_qtrle.fern.txt')
put(root/'examples/android/plex/app.fern','input/plex-app.fern.txt')
put(local/'archive.py','archive.py')
identity={'measured_pr_head':'7bd045eb04c8ea0b5980f0526e778ae107571d05','compiler_code_source':'6f6bddfc2af38b6c7e85d23d9338a10c8fb9c307','main_baseline':'4a04428264dd4641ca1032cf5edc84762a883dd5','producer_source':'a19e87208c5931d2600fdb75cc966fd179ae8c8f','producer_sha256':'a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751','ci_run':42723690303,'ci_merge_ref':'3b241a500ce94affa879210739135c1e8d56e2ed','archive_time':datetime.datetime.now(datetime.timezone.utc).isoformat(),'original_files':records,'note':'Collected before the baseline-bank commit. Compiler, stdlib and test code remain the measured 6f6 source. The new bank changes one static size baseline only. Raw records and the initial profile-analysis failure remain intact.'}
(out/'identities.json').write_text(json.dumps(identity,indent=2)+'\n')
manifest={'sha256':{str(p.relative_to(out)):sha(p.read_bytes()) for p in sorted(out.rglob('*')) if p.is_file()}}
(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
for row in records:
 data=(out/row['path']).read_bytes();raw=gzip.decompress(data) if row['gzip'] else data
 assert sha(raw)==row['original_sha256'] and len(raw)==row['original_bytes']
print('Archived data hashes',len(manifest['sha256']),'original byte identities',len(records))
