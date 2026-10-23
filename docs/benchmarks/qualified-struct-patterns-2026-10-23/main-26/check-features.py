from pathlib import Path
import json,os,re,shutil,subprocess
root=Path('/workspace/lang-qualified-struct-patterns-publish');out=Path('/workspace/scratch/lang-qualified-struct-patterns/main-26-integration')
compiler=out/'compiler';oracle=out/'oracle';stdlib=root/'internal/stdlib';fixture=root/'conformance/cases/qualified_struct_patterns/main.fern'
env={k:v for k,v in os.environ.items() if not k.startswith('FERN_')}; rows=[]
def run(name,args,expected=0,variables=None,census=False):
 r=subprocess.run(list(map(str,args)),cwd=root,env=dict(env,**(variables or {})),capture_output=True)
 row={'name':name,'arguments':list(map(str,args)),'exit':r.returncode,'stdout':r.stdout.decode(),'stderr':r.stderr.decode()};rows.append(row)
 (out/'feature-results.json').write_text(json.dumps(rows,indent=2)+'\n')
 assert r.returncode==expected,(name,row)
 if census:
  m=re.search(r'(?m)^leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)$',row['stderr'])
  assert m and m[1]==m[2] and m[3]=='0',(name,row)
 assert 'fern-sanitizer:' not in row['stderr'],(name,row)
 return row
for name,args in [('Go-check',[oracle,'-check',fixture]),('Go-interp',[oracle,'-interp',fixture]),('self-check',[compiler,'-check',fixture,stdlib]),('self-interp',[compiler,'-interp',fixture,stdlib])]:
 row=run(name,args);assert not row['stdout'] and not row['stderr'],row
flags={'FERN_STRICT_IR':'1','FERN_SANITIZE':'1','FERN_LEAKCHECK':'1'}
for target in ['x86-64-linux','arm64-linux','wasm32-wasi']:
 binary=out/('fixture-'+target)
 args=[compiler,'-target',target,'-o',binary]
 if target=='wasm32-wasi':args+=['-emit','command-module']
 run('compile-'+target,args+[fixture,stdlib],variables=flags)
 runner=[]
 if target=='arm64-linux':runner=['qemu-aarch64']
 if target=='wasm32-wasi':runner=['wasmtime','run']
 row=run('run-'+target,runner+[binary],census=True);assert not row['stdout'],row
negative=out/'negative';negative.mkdir(exist_ok=True);shutil.copyfile(fixture.parent/'shapes.fern',negative/'shapes.fern')
cases={
 'let':'function main(): i32 { let shapes.Secret { n } = shapes.secret(); return n; }',
 'parameter':'function f(shapes.Secret { n }: shapes.Secret): i32 { return n; } function main(): i32 { return f(shapes.secret()); }',
 'for':'function main(): i32 { for shapes.Secret { n } in [shapes.secret()] { return n; } return 0; }',
 'match':'function main(): i32 { match (shapes.secret()) { shapes.Secret { n } => { return n; } } }',
 'match-expression':'function main(): i32 { return match (shapes.secret()) { shapes.Secret { n } => n }; }',
 'if-let':'function main(): i32 { if let shapes.Secret { n } = shapes.secret() { return n; } return 0; }',
 'discard-field':'function main(): i32 { match (shapes.secret()) { shapes.Secret { n: _ } => { return 0; } } }'}
for name,source in cases.items():
 path=negative/(name+'.fern');path.write_text('import "./shapes";\n'+source+'\n')
 for engine,args in [('Go',[oracle,'-check',path]),('self',[compiler,'-check',path,stdlib])]:
  row=run(name+'-'+engine,args,expected=1);text=row['stdout']+row['stderr']
  assert text.count('error[')==1 and 'error[E021]: cannot destructure' in text and 'opaque type shapes.Secret outside the module' in text,row
print('All feature checks pass:',len(rows))
