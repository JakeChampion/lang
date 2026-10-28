from pathlib import Path
import subprocess,json,time,hashlib,re,sys
root=Path('/tmp/image-engine-worktrees/lang-image-arm64-large-offset-health');mode=sys.argv[1];prefix=Path('/tmp/image-arm64-large-offset-unique-label-final-'+mode)
assert not prefix.with_suffix('.json').exists()
git=lambda *a:subprocess.check_output(['git',*a],cwd=root,text=True).strip()
source=git('rev-parse','HEAD');assert source==Path('/tmp/image-arm64-large-offset-unique-label-source-sha.txt').read_text().strip() and not git('status','--porcelain')
paths=git('ls-files','compiler','internal/stdlib','internal/testing').splitlines()
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
def hashes():return {p:sha(root/p) for p in paths}
before=hashes();binary_before={p:sha(root/p) for p in ['bin/fern','bin/fern-selfhost']}
checks={
 'targets':[['go','test','-v','./internal/testing/e2ecompiler','-run','^TestSelfHostArm64(LargeMemoryOffsets|MemOffsetsMatchGas|RefusesUnencodableOffsets|AcceptsTheLastLegalOffset|LitPoolRange|AsmLiteralPool64Bit)$','-count=1','-timeout=20m']],
 'complexity':[['go','test','-v','./internal/testing/e2ecompiler','-run','^TestSelfHostRepoComplexityRatchet$','-count=1','-timeout=10m']],
 'runtime':[['make','runtime-check']],
 'aux':[['bin/fern','-fmt','-d',*[p for p in git('ls-files','*.fern','*.fern.md').splitlines() if not p.startswith('conformance/') and '/testdata/' not in p]],['go','build','./...'],['go','vet','./...'],['make','actionlint','testnames','ci-selftest','runtime-bundle-check','gofmt-check'],['go','test','-v','./internal/testing/sourcelint','-count=1'],['git','diff','--check']]
}[mode]
record=dict(source=source,worktree_clean=True,diagnostic_overlay=False,source_hashes=before,binaries=binary_before,checks=[]);started=time.time()
prefix.with_suffix('.before.json').write_text(json.dumps(record,indent=2)+'\n')
with prefix.with_suffix('.log').open('w') as log:
 for command in checks:
  t=time.time();r=subprocess.run(command,cwd=root,stdout=log,stderr=subprocess.STDOUT)
  record['checks'].append(dict(command=command,exit=r.returncode,seconds=time.time()-t));log.flush()
  prefix.with_suffix('.checkpoint.json').write_text(json.dumps(record,indent=2)+'\n')
  if r.returncode:break
s=prefix.with_suffix('.log').read_text();record.update(seconds=time.time()-started,sources_unchanged=before==hashes(),binaries_unchanged=binary_before=={p:sha(root/p) for p in binary_before},PASS=len(re.findall(r'^\s*--- PASS:',s,re.M)),FAIL=len(re.findall(r'^\s*--- FAIL:',s,re.M)),SKIP=len(re.findall(r'^\s*--- SKIP:',s,re.M)),worktree_still_clean=not git('status','--porcelain'))
prefix.with_suffix('.json').write_text(json.dumps(record,indent=2)+'\n');print({k:v for k,v in record.items() if k not in ['source_hashes','checks']});print('exits',[x['exit'] for x in record['checks']])
assert len(record['checks'])==len(checks) and all(x['exit']==0 for x in record['checks']) and record['sources_unchanged'] and record['binaries_unchanged'] and record['worktree_still_clean'] and record['FAIL']==record['SKIP']==0
if mode=='targets':assert record['PASS']>=30 and '--- PASS: TestSelfHostArm64LitPoolRange' in s
if mode=='complexity':assert record['PASS']==3
if mode=='aux':assert 'ERROR ' not in s
