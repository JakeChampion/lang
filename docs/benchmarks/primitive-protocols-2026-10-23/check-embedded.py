from pathlib import Path
import subprocess,json,shutil,re,hashlib
root=Path('/workspace/lang-primitive-protocols-current');out=Path('/workspace/scratch/lang-primitive-protocols');work=out/'embedded-project';work.mkdir(exist_ok=True);exe=out/'embedded-fern';rows=[]
for case in sorted((root/'conformance/cases').glob('primitive_*')):
 dest=work/case.name;dest.mkdir(exist_ok=True)
 for p in case.glob('*.fern'):shutil.copy2(p,dest/p.name)
 negative=(case/'expected.error').exists();expected=(case/'expected.stdout').read_text() if (case/'expected.stdout').exists() else ''
 for mode in ['-check']+([] if negative else ['-interp']):
  args=[str(exe),mode,str(dest/'main.fern')];p=subprocess.run(args,cwd='/tmp',capture_output=True,text=True)
  good=p.returncode==0 and (mode!='-interp' or p.stdout==expected)
  if negative:good=p.returncode!=0 and set(re.findall(r'error\[(E\d+)\]',p.stderr))=={(case/'expected.error').read_text().strip()}
  rows.append(dict(name=case.name,args=args,exit=p.returncode,stdout=p.stdout,stderr=p.stderr,pass_=good));print(case.name,mode,p.returncode,'PASS' if good else 'FAIL',flush=True)
  assert good,rows[-1]
 if not negative and case.name in ['primitive_display_without_imports','primitive_protocols_and_unsigned_num','primitive_display_in_imported_defaults']:
  binary=dest/'program';args=[str(exe),'-target','x86-64-linux','-o',str(binary),str(dest/'main.fern')];p=subprocess.run(args,cwd='/tmp',capture_output=True,text=True);assert p.returncode==0,p.stderr;rows.append(dict(name=case.name,args=args,exit=p.returncode,stdout=p.stdout,stderr=p.stderr,pass_=True));q=subprocess.run([str(binary)],cwd='/tmp',capture_output=True,text=True);good=q.returncode==0 and q.stdout==expected;rows.append(dict(name=case.name,args=[str(binary)],exit=q.returncode,stdout=q.stdout,stderr=q.stderr,pass_=good));assert good,rows[-1]
p=subprocess.run([str(exe),'-version'],cwd='/tmp',capture_output=True,text=True);assert p.returncode==0;pout=dict(args=[str(exe),'-version'],exit=p.returncode,stdout=p.stdout,stderr=p.stderr,pass_=True);rows.append(pout)
(out/'embedded-results.json').write_text(json.dumps(dict(source_commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),binary_sha256=hashlib.sha256(exe.read_bytes()).hexdigest(),binary_bytes=exe.stat().st_size,cwd='/tmp',commands=rows),indent=2)+'\n');print('PASS',len(rows),'commands',flush=True)
