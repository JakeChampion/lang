from pathlib import Path
import os,subprocess,json,re
root=Path('/workspace/lang-map-iterator-current');out=Path('/workspace/scratch/lang-map-iterator');compiler=out/'argument-diags-validation/bootstrap/stage3';oracle=out/'committed-oracle';rows=[]
names=['map_iter_struct_value','map_iter_unannotated','map_str_iter','map_struct_value_field','rc_corpus_map_iter_escape_churn_free','rc_corpus_map_iter_string_kv_retain_churn_free','map_iterator_protocol','iterator_nested_tuple_adapter']
flags=dict(os.environ,FERN_STRICT_IR='1',FERN_SANITIZE='1',FERN_LEAKCHECK='1')
def run(name,args,exit=0,census=False):
 p=subprocess.run(list(map(str,args)),cwd=root,env=flags,capture_output=True,text=True)
 row={'name':name,'args':list(map(str,args)),'expected_exit':exit,'exit':p.returncode,'stdout':p.stdout,'stderr':p.stderr};rows.append(row);(out/'committed-fixture-results.json').write_text(json.dumps(rows,indent=2)+'\n')
 assert p.returncode==exit,row
 if census:
  m=re.search(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',p.stderr);assert m and m[1]==m[2] and m[3]=='0',row
 assert 'fern-sanitizer:' not in p.stderr,row
for name in names:
 source=root/'conformance/cases'/name/'main.fern';expected=source.parent/'expected.exit';exit=int(expected.read_text()) if expected.exists() else 0
 for mode in ['-check','-interp']:
  run(name+'-Go'+mode,[oracle,mode,source],exit if mode=='-interp' else 0)
  run(name+'-self'+mode,[compiler,mode,source,root/'internal/stdlib'],exit if mode=='-interp' else 0)
 for target,runner in [('x86-64-linux',[]),('arm64-linux',['qemu-aarch64']),('wasm32-wasi',['wasmtime','run'])]:
  binary=out/(name+'-'+target);args=[compiler,'-target',target,'-o',binary]
  if target=='wasm32-wasi':args+=['-emit','command-module']
  run(name+'-compile-'+target,args+[source,root/'internal/stdlib']);run(name+'-run-'+target,runner+[binary],exit,True)
 print('PASS',name,flush=True)
print('All migrated fixture checks pass:',len(rows),flush=True)
