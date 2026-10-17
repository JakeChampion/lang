from pathlib import Path
import os,subprocess,json,hashlib,time,statistics
out=Path('/workspace/scratch/lang-certifier-region-final-cold-cost');out.mkdir(exist_ok=True)
compiler='/workspace/scratch/fern-certifier-region-projection-final';root=Path('/workspace/lang-coreutils-parity')
report={'compiler_sha256':hashlib.sha256(Path(compiler).read_bytes()).hexdigest(),'memo':'disabled; cold certification every run','rows':[],'ratios':[]}
cmd=[compiler,'-target','x86-64-linux','-o',str(out/'compiled'),str(root/'compiler/fern.fern'),str(root/'internal/stdlib')]
for pair in range(3):
 times={}
 for mode in (['disabled','enabled'] if pair%2==0 else ['enabled','disabled']):
  env=dict(os.environ,FERN_RC_CERTIFY_MEMO='0');env.pop('FERN_RC_CERTIFY',None)
  if mode=='disabled':env['FERN_RC_CERTIFY']='0'
  start=time.perf_counter();p=subprocess.run(cmd,capture_output=True,env=env,timeout=180);times[mode]=time.perf_counter()-start
  assert p.returncode==0,(p.returncode,p.stderr.decode())
  report['rows'].append({'pair':pair,'mode':mode,'seconds':times[mode],'output_sha256':hashlib.sha256((out/'compiled').read_bytes()).hexdigest()})
 report['ratios'].append(times['enabled']/times['disabled']);report['median_ratio']=statistics.median(report['ratios'])
 (out/'results.json').write_text(json.dumps(report,indent=2)+'\n')
 assert len(set(x['output_sha256']for x in report['rows']))==1
 print('cold certifier enabled/disabled',pair,report['ratios'][-1],report['median_ratio'],flush=True)
