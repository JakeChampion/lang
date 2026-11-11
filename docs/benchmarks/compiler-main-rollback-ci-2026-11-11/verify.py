from pathlib import Path
import argparse,subprocess,json,hashlib,gzip
p=argparse.ArgumentParser();p.add_argument('--commit',default='HEAD');p.add_argument('--worktree',action='store_true');a=p.parse_args()
root=Path(__file__).resolve().parents[3];folder=Path(__file__).resolve().parent.relative_to(root)
sha=lambda b:hashlib.sha256(b).hexdigest()
def git(*args):return subprocess.check_output(['git',*args],cwd=root)
def read(name):return(root/name).read_bytes()if a.worktree else git('show',a.commit+':'+str(name))
def data(name):return read(folder/name)
def raw(name):return gzip.decompress(data(name))
m=json.loads(data('manifest.json'));v=json.loads(data('bank-validation.json'));r=json.loads(data('ci-receipts.json'))
for name,h in m['data_sha256'].items():assert sha(data(name))==h,name
for row in m['gzip_identities']:
 b=raw(row['file']);assert len(b)==row['raw_bytes']and sha(b)==row['raw_sha256'],row['file']
assert m['source']==v['source']==r['main_commit']['sha']=='38399ecf5c363e3ef9a2981fa63cef01ff5401cb'
assert r['run']['id']==42958185608 and r['run']['status']=='completed'and r['run']['conclusion']=='success'
assert len(r['jobs'])==r['total_jobs']==139 and all(j['status']=='completed'and j['conclusion']in['success','skipped']for j in r['jobs'])
assert r['main_commit']['tree']['sha']==r['measured_commit']['tree']['sha']=='187b398684070660808d6d05db7d82b08d6e4e96'
for name,h in v['source_product_trees'].items():
 if a.worktree:assert not git('diff',m['source'],'--',name),name
 else:assert git('rev-parse',a.commit+':'+name).decode().strip()==h,name
for name,e in v['protocol'].items():assert sha(read(Path(name)))==e['sha256']and read(Path(name))==raw('protocol/'+name+'.gz'),name
for name in ['.github/cliff-baseline.txt','internal/testing/e2e/testdata/conformance-leak-census.txt','internal/testing/e2ecompiler/self_host_irverify_test.go','spec/diagnostics.md','spec/semantics.md']:
 assert read(Path(name))==raw('publication/'+name+'.gz'),name
assert v['exit_code']==0 and [(x['label'],x['exit_code'])for x in v['runs']]==[('full-old',1),('full-new',0)]
assert v['row_before']==378075 and v['row_after']==373796 and not v['full_report_missing_metrics']
assert b'MISSING:'not in raw('full-old.stdout.gz')+raw('full-old.stderr.gz')+raw('full-new.stdout.gz')+raw('full-new.stderr.gz')
report=raw('perf-artifact/cliff-report.txt.gz');assert b'39058864'in report and b'373796'in report
reuse=v['qualified_controls'];old=reuse['publication'];oldfolder=reuse['folder'];om=json.loads(git('show',old+':'+oldfolder+'/manifest.json'))
for name,h in om['data_sha256'].items():assert sha(git('show',old+':'+oldfolder+'/'+name))==h,name
assert len(om['data_sha256'])==reuse['verified_data_hashes']==37 and reuse['canonical_results']==22 and reuse['commands_executed']==0
oldm=json.loads(git('show',m['prior_publication']+':docs/benchmarks/compiler-checker-cliff-ci-2026-11-11/manifest.json'))
for name,h in oldm['data_sha256'].items():assert sha(read(Path('docs/benchmarks/compiler-checker-cliff-ci-2026-11-11')/name))==h,name
validation=json.loads(data('validation/result.json'));assert validation['exit_code']==0
assert all(x['exit_code']==0 for x in validation['runs'])
assert all(not x.get('skips',0)and not x.get('failures',[])for x in validation['runs'])
print('Verified',len(m['data_sha256']),'committed data hashes and',len(m['gzip_identities']),'raw gzip identities.')
