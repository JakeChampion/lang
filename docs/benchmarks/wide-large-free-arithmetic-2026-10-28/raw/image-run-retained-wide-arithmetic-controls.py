from pathlib import Path
import json,hashlib,subprocess,re,os,time,shutil
root=Path('/tmp/image-engine-worktrees/lang-image-retained-large-free-health');base=Path('/tmp/image-retained-wide-arithmetic-controls');base.mkdir(exist_ok=False);compiler=Path('/tmp/image-arm64-large-offset-unique-label-current-fern-3ba81dccd');sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest();compiler_sha=sha(compiler);assert compiler_sha=='b7bb59c4ccc841709950b369fc996cd04087eda762ab46549b97ad3ba48336de';entry=root/'compiler/drivers/ssalargepushwide_run.fern';todo=[entry];files={}
while todo:
 p=todo.pop().resolve();relative=str(p.relative_to(root))
 if relative in files:continue
 files[relative]=sha(p)
 for name in re.findall(r'^import "([^"]+)"',p.read_text(),re.M):
  if name.startswith('.'):
   q=(p.parent/name).with_suffix('.fern');assert q.is_file();todo.append(q)
assert len(files)==8
mutations=[('floor','return if (n < 0 as i64 && n % (4294967296 as i64) != 0 as i64) { q - 1 as i64 } else { q };','return q;','signed floor word projection'),('join','offset: low.offset + q * (4294967296 as i64)','offset: low.offset','signed word reconstruction'),('mask','number(r, low * step)','number(r, (low * step) % (4294967296 as i64))','wide rounding boundary')];record=dict(source=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),feature_uncommitted=True,compiler_build='3ba81dccd2de600e45fa9539247e28352e07118b',compiler_sha256=compiler_sha,original_closure_hashes=files,controls=[]);env=dict(os.environ,FERN_LEAKCHECK='1',FERN_SANITIZE='1',FERN_STRICT_IR='1');start=time.time()
for name,old,new,reason in mutations:
 folder=base/name;folder.mkdir();manifest={}
 for n,h in files.items():
  p=folder/n;p.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(root/n,p);assert sha(p)==h
 p=folder/'compiler/ssalargepushwidevalue.fern';text=p.read_text();assert text.count(old)==1;p.write_text(text.replace(old,new));manifest={n:sha(folder/n)for n in files};(folder/'source-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');binary=folder/'control.elf';commands=[('compile',[str(compiler),'-target','x86-64-linux','-sanitize','-o',str(binary),str(folder/'compiler/drivers/ssalargepushwide_run.fern')]),('runtime',[str(binary)])];item=dict(name=name,expected_reason=reason,source_hashes=manifest,checks=[])
 for stage,cmd in commands:
  log=folder/(stage+'.log');t=time.time()
  if stage=='runtime':item['binary_sha256_before_runtime']=sha(binary)
  with log.open('w')as out:r=subprocess.run(cmd,cwd=folder,env=env,stdout=out,stderr=subprocess.STDOUT)
  item['checks'].append(dict(stage=stage,command=cmd,exit=r.returncode,seconds=time.time()-t))
  if stage=='compile'and r.returncode:break
 item['sources_unchanged']=manifest=={n:sha(folder/n)for n in manifest};item['original_sources_unchanged']=files=={n:sha(root/n)for n in files}
 if binary.exists():item['binary_sha256_after_runtime']=sha(binary)
 log=folder/'runtime.log';s=log.read_text()if log.exists()else'';item['actual_heaps']=[list(map(int,m))for m in re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',s)];item['expected_failure_qualified']=len(item['checks'])==2 and item['checks'][0]['exit']==0 and item['checks'][1]['exit']==180 and ('wide large-push arithmetic controls failed: '+reason)in s and item.get('binary_sha256_before_runtime')==item.get('binary_sha256_after_runtime')and item['sources_unchanged']and item['original_sources_unchanged'];record['controls'].append(item);Path('/tmp/image-retained-wide-arithmetic-controls.checkpoint.json').write_text(json.dumps(record,indent=2)+'\n');print(name,item['checks'],item['expected_failure_qualified'],flush=True)
 if not item['expected_failure_qualified']:break
record.update(seconds=time.time()-start,compiler_unchanged=sha(compiler)==compiler_sha,original_sources_unchanged=files=={n:sha(root/n)for n in files},intentional_early_exit_not_persistent_leak_qualification=True);Path('/tmp/image-retained-wide-arithmetic-controls.json').write_text(json.dumps(record,indent=2)+'\n');assert len(record['controls'])==3 and all(x['expected_failure_qualified']for x in record['controls'])and record['compiler_unchanged']and record['original_sources_unchanged']
