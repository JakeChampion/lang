from pathlib import Path
import json,subprocess
out=Path('/workspace/scratch/lang-grammar');root=Path('/workspace/lang-grammar-current');rows=[]
heads=['while (false)','loop','for (let i = 0; i < 1; i = i + 1)','for x in [1]','for x in 0..1','for x in 0..=1','for (x, y) in [(1, 2)]','outer: while (false)','outer: loop','outer: for x in [1]']
for i,head in enumerate(heads):
 for block in [False,True]:
  source=out/('committed-loop-'+str(i)+'-'+str(block)+'.fern');source.write_text('function main(): i32 { '+head+(' { break; } ' if block else ' return 1; ')+'return 0; }\n')
  for cli in ['committed-oracle','committed-compiler']:
   args=[str(out/cli),'-check',str(source),str(root/'internal/stdlib')];p=subprocess.run(args,capture_output=True,text=True)
   row=dict(head=head,block=block,cli=cli,args=args,exit=p.returncode,stdout=p.stdout,stderr=p.stderr);rows.append(row)
   assert p.returncode==0 if block else p.returncode!=0 and 'error[P001]' in p.stderr,row
(out/'committed-loop-results.json').write_text(json.dumps(rows,indent=2)+'\n');print('PASS',len(rows),'loop checks')
