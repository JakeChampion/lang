from pathlib import Path
import subprocess,json,datetime
out=Path(__file__).resolve().parent;root=Path("/workspace/lang-checker-union-candidate-20261110-1600")
row={"args":["make","ci-selftest"],"source":"99fda7a649924cdc341f014f52e1d4ab9a6ad0f4","started":datetime.datetime.now(datetime.timezone.utc).isoformat()}
with(out/"ci-selftest.log").open("wb")as log:r=subprocess.run(row["args"],cwd=root,stdout=log,stderr=subprocess.STDOUT)
row.update(exit_code=r.returncode,finished=datetime.datetime.now(datetime.timezone.utc).isoformat());(out/"ci-selftest.json").write_text(json.dumps(row,indent=2)+"\n")
raise SystemExit(r.returncode)
