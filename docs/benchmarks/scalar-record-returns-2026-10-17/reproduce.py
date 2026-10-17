from pathlib import Path
import subprocess,json,hashlib,statistics,time,shutil
import sys
if len(sys.argv) != 5:
 raise SystemExit('usage: reproduce.py BASE_COMPILER CANDIDATE_COMPILER SOURCE_ROOT OUTPUT_DIR')
root=Path(sys.argv[3]).resolve()
out=Path(sys.argv[4]).resolve();out.mkdir(exist_ok=True)
compilers={'baseline':str(Path(sys.argv[1]).resolve()),'candidate':str(Path(sys.argv[2]).resolve())}
report={'source_head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'compilers':{k:hashlib.sha256(Path(v).read_bytes()).hexdigest() for k,v in compilers.items()},'rows':[],'timings':{}}
for name,entry in [('scalar-record',root/'bench/compiler/scalar_record_return.fern'),('field25519',root/'bench/crypto/field_scalar_result.fern'),('x25519',root/'bench/crypto/scalar_return_allocations.fern')]:
 checksum=None
 for target in ['x86-64-linux','arm64-linux','wasm32-wasi']:
  for tag,compiler in compilers.items():
   binary=out/f'{name}-{target}-{tag}'
   p=subprocess.run([compiler,'-target',target,'-o',str(binary),str(entry),str(root/'internal/stdlib')],capture_output=True,timeout=180)
   assert p.returncode==0,p.stderr.decode();binary.chmod(0o755)
   cmd=([shutil.which('qemu-aarch64')] if target=='arm64-linux' else [shutil.which('wasmtime'),'run'] if target=='wasm32-wasi' else [])+[str(binary)]
   p=subprocess.run(cmd,capture_output=True,timeout=180);assert p.returncode==0,(name,target,tag,p.stderr.decode())
   parts=p.stdout.decode().splitlines();assert len(parts)==2,parts
   if checksum is not None:assert checksum==parts[1]
   checksum=parts[1]
   row={'program':name,'target':target,'compiler':tag,'allocations':int(parts[0]),'checksum':parts[1],'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest()}
   report['rows'].append(row);print(row,flush=True)
  (out/'results.json').write_text(json.dumps(report,indent=2)+'\n')
 times=[]
 for pair in range(7):
  elapsed={}
  for tag in (['baseline','candidate'] if pair%2==0 else ['candidate','baseline']):
   start=time.perf_counter();p=subprocess.run([str(out/f'{name}-x86-64-linux-{tag}')],capture_output=True,timeout=60)
   elapsed[tag]=time.perf_counter()-start;assert p.returncode==0
   times.append({'pair':pair,'compiler':tag,'seconds':elapsed[tag]})
  print(name,pair,elapsed['candidate']/elapsed['baseline'],flush=True)
 ratios=[next(r['seconds'] for r in times if r['pair']==i and r['compiler']=='candidate')/next(r['seconds'] for r in times if r['pair']==i and r['compiler']=='baseline') for i in range(7)]
 report['timings'][name]={'rows':times,'paired_ratios':ratios,'median_ratio':statistics.median(ratios)}
 (out/'results.json').write_text(json.dumps(report,indent=2)+'\n')
