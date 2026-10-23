from pathlib import Path
import hashlib,json,subprocess,sys,time

root,out,compiler=map(lambda s:Path(s).resolve(),sys.argv[1:])
out.mkdir(exist_ok=True,parents=True)
source=hashlib.sha256()
for name in sorted(subprocess.check_output(['git','ls-files','compiler','internal/stdlib'],cwd=root,text=True).splitlines()):
    source.update(name.encode()+b'\0'+(root/name).read_bytes()+b'\0')
report={'source_git':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'source_sha256':source.hexdigest(),'stages':[]}
for name in ['stage1','stage2','stage3']:
    dest=out/name
    row={'stage':name,'producer_sha256':hashlib.sha256(compiler.read_bytes()).hexdigest()}
    before=time.monotonic()
    with (out/(name+'.log')).open('w') as log:
        subprocess.run([str(compiler),'-target','x86-64-linux','-o',str(dest),str(root/'compiler/fern.fern'),str(root/'internal/stdlib')],cwd=root,stdout=log,stderr=subprocess.STDOUT,check=True)
    dest.chmod(0o755)
    row.update(seconds=time.monotonic()-before,bytes=dest.stat().st_size,sha256=hashlib.sha256(dest.read_bytes()).hexdigest())
    report['stages'].append(row);print(row,flush=True)
    compiler=dest
assert (out/'stage2').read_bytes()==(out/'stage3').read_bytes()
report['fixedpoint']=True
(out/'bootstrap.json').write_text(json.dumps(report,indent=2)+'\n')
