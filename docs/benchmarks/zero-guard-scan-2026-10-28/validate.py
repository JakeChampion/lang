from pathlib import Path
import subprocess, os, shutil, json, hashlib, re
p=Path(__file__).parent
sha=lambda x:hashlib.sha256(x.read_bytes()).hexdigest()
records=[]
for label, origin in [('before',Path('/workspace/lang-perf-4412/compiler')),('after',p/'compiler')]:
    project=p/('contract-'+label)
    assert not project.exists()
    shutil.copytree(origin,project)
    (project/'contract.fern').write_bytes((p/'zero-guard-contract.fern.txt').read_bytes())
    for target in ['x86-64-linux','arm64-linux','wasm32-wasi']:
        binary=p/(label+'-contract-'+target)
        cmd=[str(p.parent/'record-pair-candidate/stage3'),'-O','-target',target,'-o',str(binary),str(project/'contract.fern'),str(p.parent/'fixed-source/internal/stdlib')]
        env=dict(os.environ, FERN_SANITIZE='1', FERN_LEAKCHECK='1', FERN_RC_CERTIFY_MEMO=str(p/'memo-contract'))
        with (p/(binary.name+'-build.log')).open('wb') as log: build=subprocess.run(cmd,env=env,stdout=log,stderr=subprocess.STDOUT)
        assert build.returncode==0,(label,target,'build',build.returncode)
        run=[str(binary)] if target=='x86-64-linux' else ['qemu-aarch64','-cpu','max',str(binary)] if target=='arm64-linux' else ['wasmtime','run',str(binary)]
        result=subprocess.run(run,capture_output=True)
        (p/(binary.name+'.out')).write_bytes(result.stdout);(p/(binary.name+'.err')).write_bytes(result.stderr)
        record={'version':label,'target':target,'source_ir_sha256':sha(project/'ir.fern'),'fixture_sha256':sha(project/'contract.fern'),'build_command':cmd,'run_command':run,'exit':result.returncode,'output_sha256':hashlib.sha256(result.stdout).hexdigest(),'output_bytes':len(result.stdout),'output_lines':len(result.stdout.splitlines()),'stderr':result.stderr.decode(errors='replace')}
        records.append(record);(p/'contract-validation.json').write_text(json.dumps(records,indent=2)+'\n');print(record,flush=True)
        assert result.returncode==0,(label,target,result.returncode,result.stderr)
        assert b'live_bytes=0' in result.stderr, result.stderr
        assert len(result.stdout.splitlines())==4050
        assert result.stdout==(p/'before-contract-x86-64-linux.out').read_bytes(),(label,target,'output mismatch')
