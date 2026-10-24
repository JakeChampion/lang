from pathlib import Path
import subprocess,json
out=Path('/workspace/scratch/lang-grammar');root=Path('/workspace/lang-grammar-current')
cases={
'mixed':('enum E { A(string), B } function main(): i32 { let e = E.A("s"); let x = if let E.A(s) = e { s } else { 3 }; return 0; }','E031'),
'escaped_binding':('enum E { A(i32), B } function main(): i32 { let e = E.A(1); let x = if let E.A(n) = e { n } else { 0 }; return n; }','E001'),
'else_binding':('enum E { A(i32), B } function main(): i32 { let e = E.B; let x = if let E.A(n) = e { n } else { n }; return 0; }','E001'),
'empty_value':('enum E { A(i32), B } function main(): i32 { let e = E.A(1); let x = if let E.A(n) = e { print("x"); } else { 0 }; return 0; }','E061'),
'missing_else':('enum E { A(i32), B } function main(): i32 { return if let E.A(n) = E.A(1) { n }; }','P001'),
'missing_comma':('enum E { A(i32), B } function main(): i32 { match (E.A(1)) { E.A(n) => print("a") _ => print("b") } return 0; }','P001'),
}
rows=[]
for name,(source,code) in cases.items():
 pth=out/('diag-'+name+'.fern');pth.write_text(source+'\n')
 for cli in ['committed-oracle','committed-compiler']:
  p=subprocess.run([str(out/cli),'-check',str(pth),str(root/'internal/stdlib')],capture_output=True,text=True)
  row=dict(name=name,cli=cli,expected=code,exit=p.returncode,stderr=p.stderr);rows.append(row)
  print(name,cli,p.returncode,p.stderr.splitlines()[0] if p.stderr else '',flush=True)
(out/'committed-diags-results.json').write_text(json.dumps(rows,indent=2)+'\n')
assert all(r['exit']!=0 and 'error['+r['expected']+']' in r['stderr'] for r in rows)
