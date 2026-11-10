from pathlib import Path
import subprocess,json,datetime,os
root=Path("/workspace/lang-checker-union-candidate-20261110-1635");out=Path(__file__).resolve().parent
state={"purpose":"Candidate source and format checks before immutable qualification.","runs":[]}
def save():(out/"quick.json").write_text(json.dumps(state,indent=2)+"\n")
try:
 for label,args in [("gofmt",["gofmt","-l","internal/testing/e2ecompiler/self_host_receiver_union_membership_test.go"]),("source-check",[os.environ["STAGE0"],"-check","compiler/fern.fern","internal/stdlib"]),("format-check",[os.environ["STAGE0"],"-fmt","-d","compiler/checker.fern"])]:
  row={"label":label,"args":args,"started":datetime.datetime.now(datetime.timezone.utc).isoformat()};state["runs"].append(row);save()
  r=subprocess.run(args,cwd=root,capture_output=True);(out/(label+".stdout")).write_bytes(r.stdout);(out/(label+".stderr")).write_bytes(r.stderr);row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0
  if label=="format-check":assert not r.stdout
 state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
