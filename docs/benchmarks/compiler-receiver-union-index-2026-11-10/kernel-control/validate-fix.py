from pathlib import Path
import subprocess,json,datetime,hashlib
out=Path(__file__).resolve().parent;root=Path('/workspace/lang-checker-union-index-20261110-1540');f=root/'internal/testing/e2ecompiler/self_host_kernel_sched_test.go';sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest();state={'base_source':'3d51e1880abc5025731f23772f1a343fe9c7fcbf','fixture_sha256':sha(f),'design':'Six fixed repetitions of both unchanged kernel scheduler assertions, then Go vet. One private one-second timer control checks migration separately and retains every original concurrency/output/frame assertion. A nonzero private-control exit remains a negative result, never a normal test pass. No compiler, stdlib, proof rule, timer or timeout change in product sources.','runs':[]}
def save():(out/'fix-validation.json').write_text(json.dumps(state,indent=2)+'\n')
save()
try:
 for label,args in [('normal-schedulers',['go','test','./internal/testing/e2ecompiler','-run','^TestSelfHostKernel(SchedulerOnQemu|TwoCoreSchedulerOnQemu)$','-count=6','-timeout=25m','-json']),('vet',['go','vet','./internal/testing/e2ecompiler'])]:
  row={'label':label,'args':args,'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save()
  with(out/(label+'.jsonl'if label=='normal-schedulers'else label+'.stdout')).open('wb')as so,(out/(label+'.stderr')).open('wb')as se:r=subprocess.run(args,cwd=root,stdout=so,stderr=se)
  row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
  if label=='normal-schedulers':
   es=[json.loads(x)for x in(out/(label+'.jsonl')).read_text().splitlines()if x.strip()];row.update(test_passes=sum(x.get('Action')=='pass'and bool(x.get('Test'))for x in es),test_failures=sum(x.get('Action')=='fail'and bool(x.get('Test'))for x in es),skips=sum(x.get('Action')=='skip'for x in es));assert row['skips']==0
  save();assert r.returncode==0
 s=f.read_text();old='src := writeTickProgram(t, smpBoardSrc, smpMainSrc)';assert s.count(old)==1;s=s.replace(old,'slowBoard := strings.Replace(smpBoardSrc, "/ 1000i64", "/ 1i64", 1)\n\tif slowBoard == smpBoardSrc { t.Fatal("timer-period precondition changed") }\n\tsrc := writeTickProgram(t, slowBoard, smpMainSrc)');overlay_source=out/'slow-timer-fixed.go';overlay_source.write_text(s);overlay=out/'slow-timer-fixed-overlay.json';overlay.write_text(json.dumps({'Replace':{str(f):str(overlay_source)}},indent=2)+'\n')
 args=['go','test','./internal/testing/e2ecompiler','-run','^TestSelfHostKernelTwoCoreSchedulerOnQemu$','-count=1','-overlay',str(overlay),'-timeout=25m','-json'];row={'label':'private-slow-timer','args':args,'started':datetime.datetime.now(datetime.timezone.utc).isoformat()};state['runs'].append(row);save()
 with(out/'fixed-slow-timer.jsonl').open('wb')as so,(out/'fixed-slow-timer.stderr').open('wb')as se:r=subprocess.run(args,cwd=root,stdout=so,stderr=se)
 es=[json.loads(x)for x in(out/'fixed-slow-timer.jsonl').read_text().splitlines()if x.strip()];outputs=''.join(x.get('Output','')for x in es);row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat(),test_failures=sum(x.get('Action')=='fail'and bool(x.get('Test'))for x in es),skips=sum(x.get('Action')=='skip'for x in es),note='The one-second private timer removes the normal concurrency trigger. All original assertion failures remain visible. Only normal unchanged-source test runs supply the product pass gate.');save();assert row['skips']==0
 # Success hides console output, while a negative private result must expose its migration flag.
 if r.returncode!=0:assert 'each on both cores true' in outputs
 assert sha(f)==state['fixture_sha256'];state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
