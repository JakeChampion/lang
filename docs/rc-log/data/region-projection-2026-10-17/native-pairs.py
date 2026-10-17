import os, subprocess, time, json, statistics, hashlib
from pathlib import Path
out=Path('/workspace/scratch/lang-certifier-region-final-pairs');out.mkdir(exist_ok=True)
root=Path('/workspace/lang-coreutils-parity')
compilers={'base':'/workspace/scratch/fern-byte-copy-report','new':'/workspace/scratch/fern-certifier-region-projection-final'}
report={'memo':'disabled; cold certification in every process','input_root':str(root),'compilers':{k:hashlib.sha256(Path(v).read_bytes()).hexdigest() for k,v in compilers.items()},'rows':[],'ratios':[]}
env=dict(os.environ,FERN_RC_CERTIFY_MEMO='0');env.pop('FERN_RC_CERTIFY',None)
for pair in range(7):
 times={}
 for tag in (['base','new'] if pair%2==0 else ['new','base']):
  binary=out/tag;start=time.perf_counter()
  subprocess.run([compilers[tag],'-target','x86-64-linux','-o',str(binary),str(root/'compiler/fern.fern'),str(root/'internal/stdlib')],capture_output=True,check=True,env=env,timeout=180)
  times[tag]=time.perf_counter()-start
  report['rows'].append({'pair':pair,'compiler':tag,'seconds':times[tag],'output_sha256':hashlib.sha256(binary.read_bytes()).hexdigest()})
 report['ratios'].append(times['new']/times['base']);report['median_ratio']=statistics.median(report['ratios'])
 (out/'results.json').write_text(json.dumps(report,indent=2)+'\n')
 assert len(set(x['output_sha256']for x in report['rows']))==1
 print(pair,report['ratios'][-1],report['median_ratio'],flush=True)
