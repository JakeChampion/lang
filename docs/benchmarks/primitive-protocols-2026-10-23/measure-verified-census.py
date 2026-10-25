from pathlib import Path
import os,subprocess,json,re,hashlib,collections
root=Path('/workspace/lang-primitive-protocols-current');out=Path('/workspace/scratch/lang-primitive-protocols');cli=out/'reachability-validation/bootstrap/stage3';work=out/'verified-census';work.mkdir(exist_ok=True);rows=[];env=dict(os.environ,FERN_RC_TRACE='1',FERN_LEAKCHECK='1',FERN_SANITIZE='1',FERN_STRICT_IR='1');pat=re.compile(r'^rctrace ([af]) ([0-9a-f]{16}) ([0-9a-f]{16}) ([0-9a-f]{16}) ([0-9a-f]{16})$',re.M)
for case in sorted((root/'conformance/cases').glob('primitive_*')):
 if (case/'expected.error').exists():continue
 binary=work/case.name;p=subprocess.run([str(cli),'-target','x86-64-linux','-o',str(binary),str(case/'main.fern'),str(root/'internal/stdlib')],env=env,capture_output=True,text=True);assert p.returncode==0,p.stderr
 q=subprocess.run([str(binary)],capture_output=True,text=True);expected=(case/'expected.stdout').read_text() if (case/'expected.stdout').exists() else '';assert q.returncode==0 and q.stdout==expected,(case.name,q.stdout,q.stderr)
 live=collections.Counter();events=pat.findall(q.stderr)
 for kind,ptr,size,site,caller in events:
  live[ptr]+=1 if kind=='a' else -1;assert live[ptr]>=0,(case.name,ptr)
 count=sum(max(0,n) for n in live.values());assert count==0 and 'live_bytes=0' in q.stderr,(case.name,q.stderr)
 rows.append(dict(name=case.name,unpaired=count,trace_events=len(events),exit=q.returncode,stdout=q.stdout,stderr=q.stderr,bytes=binary.stat().st_size,sha256=hashlib.sha256(binary.read_bytes()).hexdigest()));print(case.name,count,len(events),flush=True)
report=dict(core_commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),compiler_sha256=hashlib.sha256(cli.read_bytes()).hexdigest(),environment={k:env[k] for k in env if k.startswith('FERN_')},rows=rows);(out/'verified-census-results.json').write_text(json.dumps(report,indent=2)+'\n')
