from pathlib import Path
import json,re,collections,hashlib,datetime
out=Path(__file__).resolve().parent
raw=(out/'run.stderr').read_text()
header=re.search(r'^# fern CPU profile samples=(\d+) dropped=(\d+)$',raw,re.M)
assert header
exclusive=collections.Counter();inclusive=collections.Counter();roots=collections.Counter();depth=collections.Counter();total=0
for line in raw.splitlines():
 if not line or line.startswith('#'):continue
 stack,n=line.rsplit(' ',1);frames=stack.split(';');n=int(n)
 assert n>0
 total+=n;exclusive[frames[-1]]+=n;roots[frames[0]]+=n;depth[len(frames)]+=n
 for frame in set(frames):inclusive[frame]+=n
assert total==int(header[1])
expected=out.parent/'interpreter-main-qualification-20261109-2320/paired/second-0-candidate.stdout'
assert (out/'run.stdout').read_bytes()==expected.read_bytes()
d={'samples':total,'dropped':int(header[2]),'at_least_64_frames':sum(n for dep,n in depth.items() if dep>=64),'non_start_roots':total-roots['_start'],'exclusive':exclusive.most_common(),'inclusive':inclusive.most_common(),'roots':roots.most_common(),'depth':depth.most_common(),'tap_matches_normal_control':True,'finished':datetime.datetime.now(datetime.timezone.utc).isoformat(),'note':'Diagnostic only. Inclusive costs overlap. Deep stacks can lack the outer root. The initial controller incorrectly counted only _start-rooted rows and failed its sample-total assertion after the successful run. This separate analysis includes every folded row and verifies the header total. The original records and traceback remain unchanged.'}
(out/'breakdown.json').write_text(json.dumps(d,indent=2)+'\n')
manifest={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in out.iterdir() if p.is_file() and p.name not in ('fern-cpu','data-sha256.json')}
(out/'data-sha256.json').write_text(json.dumps(manifest,indent=2)+'\n')
print({k:d[k] for k in ('samples','dropped','at_least_64_frames','non_start_roots','tap_matches_normal_control')})
print('Exclusive top20',exclusive.most_common(20))
