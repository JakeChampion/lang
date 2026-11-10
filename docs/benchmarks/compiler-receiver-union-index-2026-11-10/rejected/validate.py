from pathlib import Path
import subprocess,json,datetime,re,os
root=Path("/workspace/lang-checker-union-candidate-20261110-1705");out=Path(__file__).resolve().parent
names=["ReceiverUnionMembership","CheckerDriverX86_64","CheckerDriverArm64","ImportedMethodCollisionX86_64","ImportedMethodCollisionArm64","ImportedMethodCollisionWasm","AmbiguousMethodRankedByImports","CheckedValueTypes","MethodEnumScrutineeIRX86_64","UnionIRPathX86_64","RepoComplexityRatchet","FeatureCensus","InterpCallableValues","InterpCallCostIgnoresFunctionCount","InterpScopeFrames","KernelSchedulerOnQemu","KernelTwoCoreSchedulerOnQemu"]
pattern="^TestSelfHost("+"|".join(names)+")$"
state={"source":"67b216378af4117cde0de6dc92a373a7636cccd2","selected_suites":["TestSelfHost"+x for x in names],"runs":[]}
def save():(out/"validation.json").write_text(json.dumps(state,indent=2)+"\n")
try:
 assert subprocess.check_output(["git","rev-parse","HEAD"],cwd=root,text=True).strip()==state["source"]
 assert not subprocess.check_output(["git","status","--porcelain"],cwd=root,text=True)
 for label,args in [("tests",["go","test","./internal/testing/e2ecompiler","-run",pattern,"-count=1","-timeout=25m","-json"]),("vet",["go","vet","./internal/testing/e2ecompiler"])]:
  row={"label":label,"args":args,"started":datetime.datetime.now(datetime.timezone.utc).isoformat()};state["runs"].append(row);save()
  with(out/(label+".jsonl" if label=="tests"else label+".stdout")).open("wb")as stdout,(out/(label+".stderr")).open("wb")as stderr:r=subprocess.run(args,cwd=root,stdout=stdout,stderr=stderr)
  row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save()
  if label=="tests":
   events=[json.loads(x)for x in(out/"tests.jsonl").read_text().splitlines()if x.strip()]
   row.update(passes=sum(x.get("Action")=="pass"for x in events),skips=sum(x.get("Action")=="skip"for x in events),failures=[x for x in events if x.get("Action")=="fail"],completed_suites=[x["Test"]for x in events if x.get("Action")=="pass"and x.get("Test")in state["selected_suites"]]);save()
   assert row["skips"]==0
   assert set(row["completed_suites"])==set(state["selected_suites"])
  assert r.returncode==0
 state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
