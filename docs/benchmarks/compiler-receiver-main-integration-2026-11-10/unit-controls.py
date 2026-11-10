from pathlib import Path
import subprocess,json,datetime
out=Path(__file__).resolve().parent;root=Path('/workspace/lang-checker-union-retained-20261110-1845');state={'source':'63d33384bb4261faa38f4436b4abf4d52a9b9542','purpose':'Actual main changes the Go parser and Paper emulator runner. Validate these integrated owner changes without a timing or Android-device claim.','runs':[]}
def save():(out/'unit-controls.json').write_text(json.dumps(state,indent=2)+'\n')
save()
try:
 for label,args in [('go-units',['go','test','./internal/syntax/parser','./internal/check/checker','-count=1','-json']),('go-syntax-units',['go','test','./internal/syntax/grammar','./internal/syntax/printer','-run','Owned|OwnResult|OwnFunc','-count=1','-json'])]:
  row={'label':label,'args':args,'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save()
  with(out/(label+'.jsonl'if label=='go-units'else label+'.stdout')).open('wb')as so,(out/(label+'.stderr')).open('wb')as se:r=subprocess.run(args,cwd=root,stdout=so,stderr=se)
  row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save()
  if label=='go-units':
   es=[json.loads(x)for x in(out/(label+'.jsonl')).read_text().splitlines()if x.strip()];row.update(test_subtest_passes=sum(x.get('Action')=='pass'and bool(x.get('Test'))for x in es),skips=sum(x.get('Action')=='skip'for x in es),failures=[x for x in es if x.get('Action')=='fail']);save();assert row['skips']==0
  assert r.returncode==0
 state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
