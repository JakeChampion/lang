from pathlib import Path
import subprocess,json,datetime,time,os,hashlib
out=Path(__file__).resolve().parent;parent=out.parent;root=Path("/workspace/lang-checker-union-candidate-20261110-1600");fixed=Path("/workspace/lang-compiler-main-20261110-1540")
state={"source":"99fda7a649924cdc341f014f52e1d4ab9a6ad0f4","fixed_input":"0e715dffcb9220479b3b6b5dc2055c81cd990497","phase":"wait-for-instruction-pair","runs":[],"reason":"Receiver checks also serve checker.annotate_module in the interpreter. Preserve the upstream function-count guard and session/callable behavior. Run its timing guard after local instrumented compilation finishes."}
def save():(out/"results.json").write_text(json.dumps(state,indent=2)+"\n")
save()
try:
 deadline=time.monotonic()+3900
 while True:
  ir=json.loads((parent/"opus-cold-instructions/results.json").read_text());assert ir.get("exit_code")in [None,0]
  if ir.get("finished"):assert ir["exit_code"]==0;break
  assert time.monotonic()<deadline;time.sleep(2)
 pattern="^TestSelfHostInterp(CallableValues|CallCostIgnoresFunctionCount|ScopeFrames)$"
 state["phase"]="tests";save();args=["go","test","./internal/testing/e2ecompiler","-run",pattern,"-count=1","-timeout=25m","-json"]
 row={"label":"tests","args":args,"started":datetime.datetime.now(datetime.timezone.utc).isoformat()};state["runs"].append(row);save()
 with(out/"tests.jsonl").open("wb")as stdout,(out/"tests.stderr").open("wb")as stderr:r=subprocess.run(args,cwd=root,stdout=stdout,stderr=stderr)
 es=[json.loads(x)for x in(out/"tests.jsonl").read_text().splitlines()if x.strip()];row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat(),test_subtest_passes=sum(x.get("Action")=="pass"and bool(x.get("Test"))for x in es),skips=sum(x.get("Action")=="skip"for x in es),failures=[x for x in es if x.get("Action")=="fail"]);save();assert r.returncode==0 and row["skips"]==0
 wanted={"TestSelfHostInterpCallableValues","TestSelfHostInterpCallCostIgnoresFunctionCount","TestSelfHostInterpScopeFrames"};assert {x.get("Test")for x in es if x.get("Action")=="pass"and x.get("Test")in wanted}==wanted
 env={k:v for k,v in os.environ.items()if not k.startswith("FERN_")};env.update(FERN_PROFILE="0",FERN_LEAKCHECK="0",FERN_RC_CERTIFY="1",FERN_RC_CERTIFY_MEMO="0")
 args=[str(parent/"candidate-normal"),"-stdlib",str(fixed/"internal/stdlib"),"-interp",str(fixed/"tests/stdlib/opus_delimited_test.fern")];row={"label":"full-Opus","args":args,"started":datetime.datetime.now(datetime.timezone.utc).isoformat(),"note":"Normal full correctness control under unchanged1980-second diagnostic limit. Not a paired speed comparison or a leak-check run."};state["runs"].append(row);state["phase"]="full-correctness";save()
 with(out/"full.stdout").open("wb")as stdout,(out/"full.stderr").open("wb")as stderr:r=subprocess.run(args,cwd=fixed,env=env,stdout=stdout,stderr=stderr,timeout=1980)
 row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat(),stdout_sha256=hashlib.sha256((out/"full.stdout").read_bytes()).hexdigest());save();assert r.returncode==0 and row["stdout_sha256"]=="07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161"
 state.update(exit_code=0,phase="complete",finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
