from pathlib import Path
import subprocess,time,json,hashlib,os,re
root=Path('/tmp/image-engine-worktrees/lang-image-arm64-large-offset-health');folder=Path('/tmp/image-arm64-large-offset-direct');folder.mkdir(exist_ok=False);sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest();compiler=root/'bin/fern';compiler_sha=sha(compiler);source_sha=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip();assert source_sha==Path('/tmp/image-arm64-large-offset-source-sha.txt').read_text().strip();assert not subprocess.check_output(['git','status','--porcelain'],cwd=root);env=dict(os.environ,FERN_SANITIZE='1',FERN_LEAKCHECK='1',FERN_STRICT_IR='1');record=dict(source=source_sha,compiler_sha256=compiler_sha,cases=[]);start=time.time()
for kind in ['record','float']:
 for count in [4095,4100,8193]:
  element,seed=('Entry','i64')if kind=='record'else('f64','f64')
  text=('struct Entry { value: i64, tag: i64 }\n'if kind=='record'else'')+f'function values(seed: {seed}): {element}[] {{ return ['
  text+=','.join((f'Entry {{ value: seed + {i} as i64, tag: {i%97} as i64 }}'if kind=='record'else f'seed + {i}.0')for i in range(count))
  text+=f']; }}\nfunction main(): i32 {{ let seed = __heap_bump_bytes() as {seed}; let a = values(seed); if (a.len() != {count}) {{ return 98; }} let i = 0; while (i < a.len()) {{'
  text+=('if (a[i].value != seed + i as i64 || a[i].tag != (i % 97) as i64) { return 99; }'if kind=='record'else'if (a[i] != seed + i as f64) { return 99; }')+' i = i + 1; }'
  for i in [0,2047,2048,4094,4095,4096,8190,8191,count-1]:
   if i<count:text+=(f'if (a[{i}].value != seed + {i} as i64 || a[{i}].tag != {i%97} as i64) {{ return 97; }}'if kind=='record'else f'if (a[{i}] != seed + {i}.0) {{ return 97; }}')
  text+='return 0; }';name=f'{kind}-{count}';src=folder/(name+'.fern');src.write_text(text);binary=folder/(name+'.elf');entry=dict(name=name,source_sha256=sha(src),checks=[])
  for stage,cmd in [('compile',[str(compiler),'-target','arm64-linux','-sanitize','-o',str(binary),str(src)]),('runtime',['/workspace/image-test-tools/qemu10/usr/bin/qemu-aarch64',str(binary)])]:
   log=folder/(name+'-'+stage+'.log');t=time.time()
   with log.open('w')as out:r=subprocess.run(cmd,cwd=root,env=env,stdout=out,stderr=subprocess.STDOUT)
   entry['checks'].append(dict(stage=stage,command=cmd,exit=r.returncode,seconds=time.time()-t,log_sha256=sha(log)))
   if r.returncode:break
  if binary.exists():entry['binary_sha256']=sha(binary)
  if len(entry['checks'])==2:
   heaps=[tuple(map(int,m))for m in re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',(folder/(name+'-runtime.log')).read_text())];entry['heaps']=heaps;entry['nonzero_balanced_zero_live']=bool(heaps)and all(a>0 and a==f and live==0 for a,f,live in heaps)
  record['cases'].append(entry);Path('/tmp/image-arm64-large-offset-direct.checkpoint.json').write_text(json.dumps(record,indent=2)+'\n')
  if any(c['exit']for c in entry['checks']):break
record.update(seconds=time.time()-start,compiler_unchanged=sha(compiler)==compiler_sha,source_still_clean=not subprocess.check_output(['git','status','--porcelain'],cwd=root));Path('/tmp/image-arm64-large-offset-direct.json').write_text(json.dumps(record,indent=2)+'\n');print({k:v for k,v in record.items()if k!='cases'});print([(x['name'],[c['exit']for c in x['checks']],x.get('heaps'))for x in record['cases']]);assert len(record['cases'])==6 and all(len(x['checks'])==2 and all(c['exit']==0 for c in x['checks'])and x['nonzero_balanced_zero_live']for x in record['cases'])and record['compiler_unchanged']and record['source_still_clean']
