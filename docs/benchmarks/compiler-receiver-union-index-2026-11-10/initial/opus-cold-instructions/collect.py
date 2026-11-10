from pathlib import Path
import datetime,hashlib,json,os,re,shutil,subprocess,time
out=Path(__file__).resolve().parent;parent=out.parent
fixed=Path("/workspace/lang-compiler-main-20261110-1540");candidate=Path("/workspace/lang-checker-union-candidate-20261110-1600")
tool=Path("/workspace/scratch/lang-valgrind-20261109/root/usr/bin/valgrind")
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
state={"baseline_source":"0e715dffcb9220479b3b6b5dc2055c81cd990497","candidate_source":"99fda7a649924cdc341f014f52e1d4ab9a6ad0f4","fixed_input_source":"0e715dffcb9220479b3b6b5dc2055c81cd990497","order":["candidate","baseline"],"runs":[],"note":"One normal guest-instruction pair for complete cold Opus compilation. Equal-length copied compiler/output paths. Targeted tests and separate counter builds may overlap. No timing claim from these instrumented runs."}
def save():
 (out/"results.tmp").write_text(json.dumps(state,indent=2)+"\n");(out/"results.tmp").replace(out/"results.json")
def run(label,args,root,env):
 row={"label":label,"args":args,"cwd":str(root),"environment":{k:v for k,v in env.items()if k.startswith("FERN_")or k=="VALGRIND_LIB"},"started":datetime.datetime.now(datetime.timezone.utc).isoformat()};state["runs"].append(row);save()
 with(out/(label+".stdout")).open("wb")as stdout,(out/(label+".stderr")).open("wb")as stderr:r=subprocess.run(args,cwd=root,env=env,stdout=stdout,stderr=stderr,timeout=1980)
 row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());save();assert r.returncode==0;return row
try:
 deadline=time.monotonic()+2100
 while True:
  prep=json.loads((parent/"prepare.json").read_text());assert prep.get("exit_code")in [None,0]
  rows=[x for x in prep["runs"]if x["label"]=="candidate-normal"]
  if rows and rows[0].get("finished"):
   assert rows[0]["exit_code"]==0;break
  assert time.monotonic()<deadline;time.sleep(2)
 env={k:v for k,v in os.environ.items()if not k.startswith("FERN_")};env.update(FERN_PROFILE="0",FERN_LEAKCHECK="0",FERN_RC_CERTIFY="1",FERN_RC_CERTIFY_MEMO="0",VALGRIND_LIB="/workspace/scratch/lang-valgrind-20261109/root/usr/libexec/valgrind")
 state["tool_version"]=subprocess.check_output([str(tool),"--version"],env=env,text=True).strip();state["producer_sha256"]=prep["producer_sha256"]
 binaries={}
 for name,folder,digest in [("baseline","base",prep["reused_baseline_normal"]["sha256"]),("candidate","cand",rows[0]["binary_sha256"])]:
  d=out/folder;d.mkdir();b=d/"fern";shutil.copy2(parent/(name+"-normal"),b);assert sha(b)==digest;binaries[name]=b
 state["binary_sha256"]={k:sha(v)for k,v in binaries.items()};state["input_sha256"]=sha(fixed/"tests/stdlib/opus_delimited_test.fern");state["stdlib_tree"]=subprocess.check_output(["git","rev-parse",state["fixed_input_source"]+":internal/stdlib"],cwd=fixed,text=True).strip();save()
 run("candidate-source-check",[str(binaries["candidate"]),"-check","compiler/fern.fern","internal/stdlib"],candidate,env)
 outputs={};counts={}
 for name in state["order"]:
  binary=binaries[name];assert sha(binary)==state["binary_sha256"][name];native=binary.parent/"opus"
  args=[str(tool),"--tool=callgrind","--cache-sim=no","--branch-sim=no","--trace-children=no","--callgrind-out-file="+str(out/(name+".callgrind")),"--log-file="+str(out/(name+".valgrind.log")),str(binary),"-target","x86-64-linux","-o",str(native),str(fixed/"tests/stdlib/opus_delimited_test.fern"),str(fixed/"internal/stdlib")]
  row=run(name,args,fixed,env)
  totals=re.findall(r"^summary:\s*(\d+)\s*$",(out/(name+".callgrind")).read_text(),re.M);refs=re.findall(r"I\s+refs:\s*([\d,]+)",(out/(name+".valgrind.log")).read_text());assert len(totals)==len(refs)==1 and int(totals[0])==int(refs[0].replace(",",""))
  counts[name]=int(totals[0]);outputs[name]=sha(native);row.update(guest_instructions=counts[name],output_sha256=outputs[name]);save()
 assert outputs["baseline"]==outputs["candidate"]
 run("candidate-program",[str(binaries["candidate"].parent/"opus")],fixed,env);assert sha(out/"candidate-program.stdout")=="07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161"
 for root in [fixed,candidate]:assert not subprocess.check_output(["git","status","--porcelain"],cwd=root,text=True)
 state.update(guest_instructions=counts,output_sha256=outputs,exit_code=0,finished=datetime.datetime.now(datetime.timezone.utc).isoformat())
except Exception as e:state.update(exit_code=1,error=repr(e),finished=datetime.datetime.now(datetime.timezone.utc).isoformat());raise
finally:save()
