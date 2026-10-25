from pathlib import Path
import re,subprocess,json,sys
root=Path('/workspace/lang-lossless-widening-current');out=Path('/workspace/scratch/lang-lossless-widening');cli=out/sys.argv[1]
s=(root/'internal/check/checker/lossless_widening_test.go').read_text();valid=re.findall(r'\{"([^"]+)", `([^`]+)`\}',s)
for a,b in [('i32','i64'),('u8','u32'),('u8','u64'),('u8','i32'),('u8','i64'),('u32','u64'),('u32','i64'),('f32','f64')]:valid.append((a+'-to-'+b,'function f(n: '+a+'): '+b+' { return n; }'))
invalid=re.findall('`([^`]+)`',s.split('func TestLosslessWideningRejectsRepresentationChanges')[1]);rows=[]
for okay,cases in [(True,valid),(False,[(str(i),src) for i,src in enumerate(invalid)])]:
 for name,src in cases:
  pth=out/('probe-'+('good-' if okay else 'bad-')+name+'.fern');pth.write_text(src+'\n');p=subprocess.run([str(cli),'-check',str(pth),str(root/'internal/stdlib')],capture_output=True,text=True,cwd=root,timeout=45)
  row=dict(name=name,expected_accept=okay,exit=p.returncode,stdout=p.stdout,stderr=p.stderr,pass_=(p.returncode==0)==okay);rows.append(row)
  if not row['pass_']:print('FAIL',row,flush=True)
(out/(sys.argv[1]+'-checks.json')).write_text(json.dumps(rows,indent=2)+'\n');print('PASS',sum(r['pass_'] for r in rows),'of',len(rows),flush=True)
assert all(r['pass_'] for r in rows)
