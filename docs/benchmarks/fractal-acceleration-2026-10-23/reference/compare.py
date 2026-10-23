from pathlib import Path
import hashlib
import json
import statistics
import subprocess
import argparse
parser=argparse.ArgumentParser()
parser.add_argument("--fern", type=Path, required=True, help="Baseline compiler used to build fern-kernel")
options=parser.parse_args()

root=Path(__file__).resolve().parent
runs={'fern': [],'same': [],'orange': []}
for i in range(5):
    names=['fern','same','orange']
    order=names[i%3:]+names[:i%3]
    for name in order:
        output=subprocess.check_output([str(root/(name+'-kernel'))],text=True)
        values={}
        for line in output.splitlines():
            case,ns,total=line.split(',')
            values[case]={'ms':int(ns)/1e6,'normalized_iterations':int(total)}
        runs[name].append(values)
summary={}
for case,*_ in json.loads((root/'cases.json').read_text()):
    fern=statistics.median(r[case]['ms'] for r in runs['fern'])
    orange=statistics.median(r[case]['ms'] for r in runs['orange'])
    same=statistics.median(r[case]['ms'] for r in runs['same'])
    summary[case]={'fern_ms':fern,'same_ms':same,'orange_ms':orange,'ratio':fern/orange,'compiler_ratio':fern/same}
    print(f'{case}: Fern {fern:.2f} ms; same kernel C++ {same:.2f} ms; adapted AVX2 reference {orange:.2f} ms; ratio {fern/orange:.2f}x')
raw={name:(root/(name+'.bin')).read_bytes() for name in runs}
assert raw['fern']==raw['same']==raw['orange']
report={'upstream_revision':'royward/optimized-mandelbrot@1051f96ee98ad347e153f56cc71dd4b8403e911a', 'source_sha256':{str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for pattern in ['orange/*','adapted/*','fern/*.fern','same-kernel.cpp'] for p in root.glob(pattern) if p.is_file()}, 'scope':'Single-thread escape computation only; no color calculation or PNG encoding. Both store per-pixel escape count and magnitude. Interior counts normalized to the per-tile limit.', 'cases':json.loads((root/'cases.json').read_text()),'runs':runs,'summary':summary,'all_samples_match':True,'samples':9*256*256,'sample_bytes_sha256':hashlib.sha256(raw['fern']).hexdigest(),'compiler_sha256':hashlib.sha256(options.fern.read_bytes()).hexdigest()}
(root/'results.json').write_text(json.dumps(report,indent=2)+'\n')
