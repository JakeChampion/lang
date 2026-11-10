from pathlib import Path
import os,subprocess,json,hashlib,datetime,shutil
out=Path(__file__).resolve().parent
base=Path("/workspace/lang-compiler-main-20261110-1540");candidate=Path("/workspace/lang-checker-union-candidate-20261110-1600")
producer=Path(os.environ["STAGE0"]);sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
state={"baseline_source":"0e715dffcb9220479b3b6b5dc2055c81cd990497","candidate_source":"99fda7a649924cdc341f014f52e1d4ab9a6ad0f4","fixed_input_source":"0e715dffcb9220479b3b6b5dc2055c81cd990497","producer_sha256":sha(producer),"runs":[]}
def save():(out/"prepare.json").write_text(json.dumps(state,indent=2)+"\n")
try:
 assert state["producer_sha256"]=="a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751"
 for root,key in [(base,"baseline_source"),(candidate,"candidate_source")]:
  assert subprocess.check_output(["git","rev-parse","HEAD"],cwd=root,text=True).strip()==state[key]
  assert not subprocess.check_output(["git","status","--porcelain"],cwd=root,text=True)
 original=out.parent/"baseline-profile-20261110-1540/normal";profile=json.loads((original.parent/"result.json").read_text());assert profile.get("finished")and profile["exit_code"]==0
 assert sha(original)==profile["normal_sha256"];shutil.copyfile(original,out/"baseline-normal");assert sha(out/"baseline-normal")==sha(original)
 state["reused_baseline_normal"]={"path":str(original),"sha256":sha(original),"build_record":str(original.parent/"result.json"),"note":"Exact completed baseline build reused, not rerun."};save()
 for label,root,leak in [("candidate-normal",candidate,"0"),("baseline-count",base,"1"),("candidate-count",candidate,"1")]:
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
