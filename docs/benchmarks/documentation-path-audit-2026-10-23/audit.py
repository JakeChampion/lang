from pathlib import Path
import re,json,glob
root=Path('/workspace/lang-doc-paths-current');docs=[root/'README.md',*sorted((root/'docs').rglob('*.md')),*sorted((root/'spec').rglob('*.md')),*sorted((root/'internal/syntax/diag/explanations').glob('*.md'))]
roots={'internal','compiler','docs','spec','scripts','site','examples','conformance','bench','benchmarks','bootstrap','runtime','cmd','tools','.github','records','contexts','measurements','proposals','rc-log','ssa-log','compiler-log','string-log','coreutils-log','typed-log','ci-log'}
rows=[]
for p in docs:
 rel=p.relative_to(root).as_posix()
 if rel.startswith(('docs/rc-log/','docs/ssa-log/')):continue
 text=p.read_text();tag=re.search(r'^Status: \[([a-z]+)\]', '\n'.join(text.splitlines()[:15]), re.M)
 status=tag[1] if tag and tag[1] in {'policy','reference','tracker','plan','research','record'} else ('record' if re.search(r'20\d\d-\d\d-\d\d',rel) else 'reference')
 if rel in ['README.md','docs/README.md'] or rel.startswith(('spec/','internal/syntax/diag/explanations/')):status='reference'
 fence=''
 for n,line in enumerate(text.splitlines(),1):
  fm=re.match(r'^\s*(`{3,}|~{3,})',line)
  if fm:
   if not fence:fence=fm[1][0]
   elif fm[1][0]==fence:fence=''
   continue
  if fence:continue
  for m in re.finditer(r'(?<!`)`([^`\n]+)`(?!`)',line):
   token=m[1]
   if any(c in token for c in ' \t\n()=<>|'):continue
   path=re.sub(r':(?:\d+(?:[-–]\d+)?|[A-Za-z_][A-Za-z_0-9]*)$','',token).rstrip('.,;')
   if path.startswith('./'):path=path[2:]
   if '/' in path and (path.startswith('docs/') or path.split('/')[0] in roots):
    target=root/path
   elif path.endswith('.md') and not path.endswith('.fern.md') and not path.startswith('YYYY-'):
    if re.fullmatch(r'\.[A-Za-z]+\.md',path):continue
    target=root/path if (root/path).exists() else p.parent/path
   else:continue
   if path.endswith('/...'):target=Path(str(target)[:-4])
   candidates=[target,p.parent/path,root/'docs'/path]
   def expand_braces(v):
    m=re.search(r'\{([^{}]+)\}',v)
    if not m:return [v]
    return [q for arm in m[1].split(',') for q in expand_braces(v[:m.start()]+arm+v[m.end():])]
   expanded=[expand_braces(str(c)) for c in candidates]
   def exists(v):
    if Path(v).exists() or (any(c in v for c in '*?[') and glob.glob(v)):return True
    q=re.sub(r'\.[A-Z][A-Za-z_0-9]*$','',v)
    return q!=v and Path(q).is_dir()
   if any(all(exists(v) for v in group) for group in expanded):continue
   rows.append(dict(file=rel,line=n,status=status,path=token,text=line.strip()))
out=Path('/workspace/scratch/lang-doc-paths/audit.json');out.write_text(json.dumps(rows,indent=2)+'\n')
from collections import Counter
live=[x for x in rows if x['status'] in ['reference','policy','tracker']]
print('Missing paths in living documentation:',len(live));print('Historical or proposed references:',len(rows)-len(live));print(dict(Counter(x['status'] for x in rows)))
for r in [x for x in rows if x['status'] in ['reference','policy','tracker']][:35]:print(f"{r['file']}:{r['line']} [{r['status']}] {r['path']}")

raise SystemExit(1 if live else 0)
