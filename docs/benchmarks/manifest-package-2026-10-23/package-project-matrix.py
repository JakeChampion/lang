from pathlib import Path
import subprocess,os,json,re,hashlib
root=Path('/workspace/lang-manifest-package-current');out=Path('/workspace/scratch/lang-manifest-package');work=out/'package-project';(work/'sub').mkdir(parents=True,exist_ok=True)
(work/'fern.toml').write_text('[package]\nname = "app"\n')
(work/'sub/fern.toml').unlink(missing_ok=True)
(work/'sub/lib.fern').write_text('pub(package) const VALUE: i32 = 42;\npub(package) struct Item { value: i32 }\npub(package) enum Choice { Chosen(Item) }\npub(package) function make(item: Item): Choice { return Chosen(item); }\n')
(work/'main.fern').write_text('import "./sub/lib";\nfunction main(): i32 { let item: lib.Item = lib.Item { value: lib.VALUE }; let choice: lib.Choice = lib.make(item); match(choice) { lib.Chosen(got) => { return got.value; } } return 1; }\n')
selfcli=out/'committed-validation/bootstrap/stage3';oracle=out/'final-oracle';rows=[];env=dict(os.environ,FERN_SANITIZE='1',FERN_LEAKCHECK='1',FERN_STRICT_IR='1')
def run(name,args,want=0,refused=False,census=False):
 p=subprocess.run(list(map(str,args)),cwd=root,env=env,capture_output=True,text=True,timeout=90)
 good=p.returncode==want if not refused else p.returncode!=0 and ('pub(package)' in p.stderr or 'not visible' in p.stderr)
 if census:
  m=re.search(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',p.stderr);good=good and bool(m and m[1]==m[2] and m[3]=='0')
 row=dict(name=name,args=list(map(str,args)),exit=p.returncode,stdout=p.stdout,stderr=p.stderr,pass_=good);rows.append(row);assert good,row
for tag,cli in [('go',oracle),('self',selfcli)]:
 run(tag+'-same-manifest-check',[cli,'-check',work/'main.fern',root/'internal/stdlib'])
 run(tag+'-same-manifest-interp',[cli,'-interp',work/'main.fern',root/'internal/stdlib'],42)
for target,runner in [('x86-64-linux',[]),('arm64-linux',['qemu-aarch64']),('wasm32-wasi',['wasmtime','run'])]:
 binary=out/('package-project-'+target);args=[selfcli,'-target',target,'-o',binary]
 if target=='wasm32-wasi':args+=['-emit','command-module']
 run('same-manifest-build-'+target,args+[work/'main.fern',root/'internal/stdlib'])
 run('same-manifest-run-'+target,runner+[binary],42,census=True)
(work/'sub/fern.toml').write_text('[package]\nname = "app"\n')
for tag,cli in [('go',oracle),('self',selfcli)]:
 for mode in ['-check','-interp']:run(tag+'-nested-same-name-'+mode,[cli,mode,work/'main.fern',root/'internal/stdlib'],refused=True)
for target in ['x86-64-linux','arm64-linux','wasm32-wasi']:
 binary=out/('refused-package-'+target);assert not binary.exists();run('nested-same-name-build-'+target,[selfcli,'-target',target,'-o',binary,work/'main.fern',root/'internal/stdlib'],refused=True);assert not binary.exists()
(work/'sub/fern.toml').unlink();(work/'fern.toml').unlink()
for tag,cli in [('go',oracle),('self',selfcli)]:run(tag+'-directory-fallback',[cli,'-check',work/'main.fern',root/'internal/stdlib'],refused=True)
report=dict(core_commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),compiler_sha256=hashlib.sha256(selfcli.read_bytes()).hexdigest(),oracle_sha256=hashlib.sha256(oracle.read_bytes()).hexdigest(),environment={k:env[k] for k in ['FERN_STRICT_IR','FERN_SANITIZE','FERN_LEAKCHECK']},commands=rows)
(out/'package-project-matrix-results.json').write_text(json.dumps(report,indent=2)+'\n');print('PASS',len(rows),'commands')
