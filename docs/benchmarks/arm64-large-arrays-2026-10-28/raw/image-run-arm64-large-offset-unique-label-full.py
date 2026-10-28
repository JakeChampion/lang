from pathlib import Path
import hashlib, json, os, re, shlex, subprocess, sys

root=Path('/tmp/image-engine-worktrees/lang-image-arm64-large-offset-health')
mode=sys.argv[1]
expected=Path('/tmp/image-arm64-large-offset-unique-label-source-sha.txt').read_text().strip()
compiler=root/'bin/fern'
prefix=Path('/tmp/image-arm64-large-offset-unique-label-full-'+mode)
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def inventory():
    files=subprocess.check_output(['git','ls-files','compiler','internal/stdlib','fern-image'],cwd=root,text=True).splitlines()
    return {p:sha(root/p)for p in files if p.endswith(('.fern','.fern.md','.sh','.yml'))}
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()==expected
assert not subprocess.check_output(['git','status','--porcelain'],cwd=root)
assert sha(compiler)==json.loads(Path('/tmp/image-arm64-large-offset-unique-label-build.json').read_text())['binaries']['bin/fern']
before=inventory();env=dict(os.environ,FERN=str(compiler),FERN_SANITIZE='1',FERN_LEAKCHECK='1')
record=dict(source_commit=expected,compiler_sha256=sha(compiler),source_hashes=before,mode=mode,commands=[])
with prefix.with_suffix('.log').open('w')as log:
    if mode in ['native','wasm']:
        cmd=['bash','fern-image/scripts/test'+('-wasm'if mode=='wasm'else'')+'.sh']
        result=subprocess.run(cmd,cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT)
        record['commands'].append(dict(command=cmd,exit_code=result.returncode))
    else:
        script=(root/'fern-image/scripts/test-wasm.sh').read_text()
        sources=shlex.split(re.search(r'^for source in (.+); do$',script,re.M).group(1))+['tests/jpeg_exif_test.fern','tests/jpeg_orientation_test.fern','tests/icc_envelope_test.fern','tests/icc_tag_data_test.fern','tests/png_incremental_test.fern','tests/icc_curv_eval_test.fern','tests/icc_parametric_eval_test.fern','tests/icc_app2_test.fern','tests/icc_matrix_eval_test.fern','tests/icc_matrix_model_test.fern','tests/icc_matrix_trc_eval_test.fern','tests/icc_matrix_solve_test.fern','tests/icc_curv_inverse_test.fern','tests/icc_power_inverse_test.fern','tests/icc_affine_inverse_test.fern','tests/icc_quadratic_inverse_test.fern','tests/icc_inverse_test.fern']
        assert len(sources)==len(set(sources))==46
        (root/'fern-image/build').mkdir(exist_ok=True)
        for n,source in enumerate(sources):
            binary=root/f'fern-image/build/arm64-large-offset-unique-label-full-{n}'
            cmd=[str(compiler),'-target','arm64-linux','-sanitize','-o',str(binary),source]
            result=subprocess.run(cmd,cwd=root/'fern-image',env=env,stdout=log,stderr=subprocess.STDOUT)
            record['commands'].append(dict(command=cmd,exit_code=result.returncode))
            if result.returncode:break
            cmd=['/workspace/image-test-tools/qemu10/usr/bin/qemu-aarch64',str(binary)]
            result=subprocess.run(cmd,cwd=root/'fern-image',env=env,stdout=log,stderr=subprocess.STDOUT)
            record['commands'].append(dict(command=cmd,exit_code=result.returncode))
            if result.returncode:break
text=prefix.with_suffix('.log').read_text()
heaps=[tuple(map(int,h))for h in re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)',text)]
record.update(exit_code=result.returncode,sources_unchanged=before==inventory()and sha(compiler)==record['compiler_sha256'],
              TAP_ok=sum(l.startswith('ok ')for l in text.splitlines()),TAP_not_ok=sum(l.startswith('not ok ')for l in text.splitlines()),
              censuses=len(heaps),balanced_zero_live=bool(heaps)and all(a>0 and a==f and live==0 for a,f,live in heaps))
prefix.with_suffix('.json').write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps({k:v for k,v in record.items()if k not in ['source_hashes','commands']}))
assert record['exit_code']==0 and record['sources_unchanged']and record['TAP_ok']==326 and record['TAP_not_ok']==0 and record['balanced_zero_live']
