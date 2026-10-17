from pathlib import Path
import subprocess,os,json,hashlib,statistics,time,shutil
import sys
if len(sys.argv) != 5:
 raise SystemExit('usage: reproduce.py BASE_COMPILER CANDIDATE_COMPILER SOURCE_ROOT OUTPUT_DIR')
root=Path(sys.argv[3]).resolve()
out=Path(sys.argv[4]).resolve();out.mkdir(exist_ok=True)
compilers={'base':str(Path(sys.argv[1]).resolve()),'new':str(Path(sys.argv[2]).resolve())}
report={'source_head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'compilers':{k:hashlib.sha256(Path(v).read_bytes()).hexdigest() for k,v in compilers.items()},'parity':[],'instructions':[],'times':[]}
def save(): (out/'results.json').write_text(json.dumps(report,indent=2)+'\n')
def env(mode='enabled',reporting=False):
 e=dict(os.environ,FERN_RC_CERTIFY_MEMO='0');e.pop('FERN_RC_CERTIFY',None)
 if mode=='disabled':e['FERN_RC_CERTIFY']='0'
 if reporting:e['FERN_RC_CERTIFY']='report'
 return e
def command(compiler,target,dest,entry='compiler/fern.fern'):
 return [compiler,'-target',target,'-o',str(dest)]+(['-embed',str(root/'internal/stdlib'),'-emit','core-module'] if entry=='compiler/playground_run.fern' else [])+[str(root/entry),str(root/'internal/stdlib')]
if (out/'results.json').exists():
 old=json.loads((out/'results.json').read_text())
 assert old['source_head']==report['source_head'] and old['compilers']==report['compilers']
 report=old
for target,entry in [('x86-64-linux','compiler/fern.fern'),('arm64-linux','compiler/fern.fern'),('wasm32-wasi','compiler/playground_run.fern')]:
 if len([x for x in report['parity'] if x['target']==target])==2:continue
 rows=[]
 for tag,compiler in compilers.items():
  dest=out/(target+'-'+tag)
  with Path(str(dest)+'.report').open('wb') as err:
   subprocess.run(command(compiler,target,dest,entry),env=env(reporting=True),stdout=subprocess.DEVNULL,stderr=err,check=True,timeout=300)
  rows.append({'target':target,'entry':entry,'compiler':tag,'output_sha256':hashlib.sha256(dest.read_bytes()).hexdigest(),'report_sha256':hashlib.sha256(Path(str(dest)+'.report').read_bytes()).hexdigest(),'report_lines':len(Path(str(dest)+'.report').read_text().splitlines())})
 assert rows[0]['output_sha256']==rows[1]['output_sha256'],rows
 assert rows[0]['report_sha256']==rows[1]['report_sha256'],rows
 report['parity']+=rows;save();print('parity',target,rows[-1],flush=True)
assert (out/'x86-64-linux-new').read_bytes()==Path(compilers['new']).read_bytes(),'candidate did not reach a fixed point'
report['fixedpoint']={'bytes':Path(compilers['new']).stat().st_size,'sha256':report['compilers']['new']};save()
for tag,compiler in compilers.items():
 dest=out/(tag+'-checker.s');cmd=[compiler,'-target','x86-64-linux','-emit','asm','-o',str(dest),str(root/'compiler/checker.fern'),str(root/'internal/stdlib')]
 with (out/(tag+'-cachegrind.log')).open('wb') as err:
  subprocess.run(['valgrind','--tool=cachegrind','--cache-sim=no','--branch-sim=no','--cachegrind-out-file='+str(out/(tag+'.cg'))]+cmd,env=env(),stdout=subprocess.DEVNULL,stderr=err,check=True,timeout=600)
 summary=next(x for x in (out/(tag+'.cg')).read_text().splitlines() if x.startswith('summary:'))
 report['instructions'].append({'compiler':tag,'instructions':int(summary.split()[1]),'output_sha256':hashlib.sha256(dest.read_bytes()).hexdigest()});save();print('instructions',report['instructions'][-1],flush=True)
assert report['instructions'][0]['output_sha256']==report['instructions'][1]['output_sha256']
report['instruction_ratio']=report['instructions'][1]['instructions']/report['instructions'][0]['instructions'];save()
for pair in range(7):
 modes=['base','new'] if pair%2==0 else ['new','base']
 if pair<3:modes+=['disabled']
 for tag in modes:
  compiler=compilers['new'] if tag=='disabled' else compilers[tag];dest=out/'timed-compiler'
  start=time.perf_counter();p=subprocess.run(command(compiler,'x86-64-linux',dest),env=env('disabled' if tag=='disabled' else 'enabled'),capture_output=True,timeout=300);seconds=time.perf_counter()-start
  assert p.returncode==0,(tag,p.returncode,p.stderr.decode())
  assert hashlib.sha256(dest.read_bytes()).hexdigest()==report['fixedpoint']['sha256']
  report['times'].append({'pair':pair,'mode':tag,'seconds':seconds})
 rows=report['times'];value=lambda i,tag:next(x['seconds'] for x in rows if x['pair']==i and x['mode']==tag)
 report['paired_ratios']=[value(i,'new')/value(i,'base') for i in range(pair+1)]
 report['median_ratio']=statistics.median(report['paired_ratios'])
 report['cold_ratios']=[value(i,'new')/value(i,'disabled') for i in range(min(pair+1,3))]
 report['cold_median_ratio']=statistics.median(report['cold_ratios']);save();print('timing',pair,report['median_ratio'],report['cold_median_ratio'],flush=True)
print('validated',report['source_head'],flush=True)
