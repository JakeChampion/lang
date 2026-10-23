from pathlib import Path
import hashlib,json,os,re,subprocess,sys

if len(sys.argv)!=5:
    raise SystemExit('usage: reproduce.py BASE_COMPILER CANDIDATE_COMPILER STDLIB OUTPUT')
base,candidate,stdlib,out=map(lambda s:Path(s).resolve(),sys.argv[1:])
out.mkdir(exist_ok=True,parents=True)
src=Path(__file__).with_name('probe.fern')
cpu=int(os.environ.get('FERN_BENCH_CPU','0'))
os.sched_setaffinity(0,{cpu})
report={'cpu':[cpu],'iterations':1000000,'source_sha256':hashlib.sha256(src.read_bytes()).hexdigest(),'checksum':'888000000\n','rows':{}}
for tag,folder,compiler in [('baseline','base',base),('candidate','cand',candidate)]:
    d=out/folder;d.mkdir(exist_ok=True);program=d/'probe'
    subprocess.run([str(compiler),'-target','x86-64-linux','-o',str(program),str(src),str(stdlib)],capture_output=True,check=True)
    p=subprocess.run([str(program)],capture_output=True,check=True)
    assert p.stdout==b'888000000\n' and not p.stderr,p
    cg=d/'cachegrind.out'
    r=subprocess.run(['valgrind','--tool=cachegrind','--cache-sim=no','--branch-sim=no','--cachegrind-out-file='+str(cg),str(program)],capture_output=True,check=True)
    assert r.stdout==p.stdout
    (d/'valgrind.log').write_bytes(r.stderr)
    count=int(re.search(r'^summary:\s+(\d+)',cg.read_text(),re.M).group(1))
    report['rows'][tag]={'compiler_sha256':hashlib.sha256(compiler.read_bytes()).hexdigest(),'binary_sha256':hashlib.sha256(program.read_bytes()).hexdigest(),'instructions':count}
(out/'results.json').write_text(json.dumps(report,indent=2)+'\n')
