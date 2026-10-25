from pathlib import Path
import subprocess,json,re,hashlib
root=Path('/workspace/lang-primitive-protocols-current');out=Path('/workspace/scratch/lang-primitive-protocols');cli=out/'reachability-validation/bootstrap/stage3';oracle=out/'reachability-oracle';rows=[]
for case in sorted((root/'conformance/cases').glob('primitive_*')):
 if not (case/'expected.error').exists():continue
 code=(case/'expected.error').read_text().strip()
 for tag,exe in [('go',oracle),('self',cli)]:
  args=[str(exe),'-check',str(case/'main.fern'),str(root/'internal/stdlib')];p=subprocess.run(args,capture_output=True,text=True);codes=set(re.findall(r'error\[(E\d+)\]',p.stderr));good=p.returncode!=0 and codes=={code};rows.append(dict(name=case.name,compiler=tag,args=args,exit=p.returncode,stdout=p.stdout,stderr=p.stderr,pass_=good));print(case.name,tag,p.returncode,codes,'PASS' if good else 'FAIL',flush=True)
report=dict(core_commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),binaries={str(p):dict(bytes=p.stat().st_size,sha256=hashlib.sha256(p.read_bytes()).hexdigest()) for p in [cli,oracle]},commands=rows)
(out/'verified-negative.json').write_text(json.dumps(report,indent=2)+'\n');assert all(r['pass_'] for r in rows)
