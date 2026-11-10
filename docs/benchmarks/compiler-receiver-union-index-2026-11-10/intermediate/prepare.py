from pathlib import Path
import os,subprocess,json,hashlib,datetime,shutil
out=Path(__file__).resolve().parent
base=Path("/workspace/lang-compiler-main-20261110-1635");candidate=Path("/workspace/lang-checker-union-candidate-20261110-1635")
producer=Path(os.environ["STAGE0"]);sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
state={"baseline_source":"515ddee8d0c06db2b94b4f3a86f3bf1c59cd2909","candidate_source":"3d51e1880abc5025731f23772f1a343fe9c7fcbf","fixed_input_source":"515ddee8d0c06db2b94b4f3a86f3bf1c59cd2909","producer_sha256":sha(producer),"runs":[]}
def save():(out/"prepare.json").write_text(json.dumps(state,indent=2)+"\n")
try:
 assert state["producer_sha256"]=="a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751"
 for root,key in [(base,"baseline_source"),(candidate,"candidate_source")]:
  assert subprocess.check_output(["git","rev-parse","HEAD"],cwd=root,text=True).strip()==state[key]
  assert not subprocess.check_output(["git","status","--porcelain"],cwd=root,text=True)
 for label,root,leak in [("baseline-normal",base,"0"),("candidate-normal",candidate,"0"),("baseline-count",base,"1"),("candidate-count",candidate,"1")]:
  env={k:v for k,v in os.environ.items()if not k.startswith("FERN_")};env.update(FERN_PROFILE="0",FERN_LEAKCHECK=leak,FERN_RC_CERTIFY="1",FERN_RC_CERTIFY_MEMO="0")
  args=[str(producer),"-target","x86-64-linux","-o",str(out/label),"compiler/fern.fern","internal/stdlib"]
  row={"label":label,"args":args,"cwd":str(root),"environment":{k:v for k,v in env.items()if k.startswith("FERN_")},"started":datetime.datetime.now(datetime.timezone.utc).isoformat()};state["runs"].append(row);save()
  with(out/(label+".stdout")).open("wb")as stdout,(out/(label+".stderr")).open("wb")as stderr:r=subprocess.run(args,cwd=root,env=env,stdout=stdout,stderr=stderr,timeout=1980)
  row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0
  row["binary_sha256"]=sha(out/label);save()
 for root in [base,candidate]:assert not subprocess.check_output(["git","status","--porcelain"],cwd=root,text=True)
 state.update(exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
