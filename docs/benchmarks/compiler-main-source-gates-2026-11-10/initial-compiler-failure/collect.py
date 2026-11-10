from pathlib import Path
import datetime,hashlib,json,os,subprocess
root=Path('/workspace/lang-free-wide-requalification-20261110-1015');out=Path(__file__).resolve().parent
compiler=Path('/workspace/lang-interpreter-dispatch-20261109/build/compiler-main-validation-20261110-0950/normal')
source='93942727d19bcad77d7aef70da15b36c58056bf8'
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
state={'source':source,'compiler_sha256':sha(compiler),'runs':[],'purpose':'Requalify unchanged exact wide-class outputs after two source provenance hashes move. No proof or negative-control changes.'}
def save():(out/'result.json').write_text(json.dumps(state,indent=2)+'\n')
try:
 assert state['compiler_sha256']=='11407d36edde3ce430806655b5f7506e84f32962d671d22777fd14abe9013f61'
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()==source
 assert not subprocess.check_output(['git','status','--porcelain'],cwd=root,text=True)
 for target in ['x86-64-linux','arm64-linux','wasm32-wasi']:
  args=['python3',str(root/'scripts/qualify_large_free_wide_classes.py'),'--compiler',str(compiler),'--target',target,'--reference',str(out/'reference-current.json.gz'),'--source-commit',source,'--output',str(out/(target+'.json')),'--work',str(out/(target+'-workers'))]
  row={'target':target,'args':args,'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save()
  with(out/(target+'.stdout')).open('wb')as stdout,(out/(target+'.stderr')).open('wb')as stderr:row['exit_code']=subprocess.run(args,cwd=root,stdout=stdout,stderr=stderr).returncode
  row['finished']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();assert row['exit_code']==0
  result=json.loads((out/(target+'.json')).read_text());assert result['qualified']and result['sources_unchanged']and result['compiler_unchanged'];assert result['case_count']==len(result['records'])==1566;assert len(result['workers'])==196
  assert all(w['qualified']and w['heap']['allocs']>0 and w['heap']['allocs']==w['heap']['frees']and w['heap']['live_bytes']==0 for w in result['workers'])
 state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as error:
 state.update(exit_code=1,error=repr(error),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
