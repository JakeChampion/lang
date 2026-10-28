from pathlib import Path
import os, subprocess, hashlib, json, time, re

p = Path(__file__).parent
root = p.parent
sha = lambda f: hashlib.sha256(f.read_bytes()).hexdigest()
stage0 = Path('/home/agent/.cache/fern/stage0/stage0-20261028-9193717/fern-selfhost-x86-64-linux')
assert sha(stage0) == '199cdd36374c8254741c2a525ae3ae2ccea2d106cb67a9ec85f568df5569f2a2'
stdlib = root / 'fixed-source/internal/stdlib'
subject = root / 'fixed-source/compiler/checker.fern'
env = dict(os.environ, FERN_RC_CERTIFY_MEMO=str(p / 'memo-build'), VALGRIND_LIB=str(root / 'tools/usr/libexec/valgrind'))
records = []
for name, producer, flags in [('stage1', stage0, []), ('stage2', p/'stage1', []), ('stage2-g', p/'stage1', ['-g']), ('stage3', p/'stage2', [])]:
    cmd = [str(producer), '-O'] + flags + ['-target', 'x86-64-linux', '-o', str(p/name), str(p/'compiler/fern.fern'), str(stdlib)]
    start = time.perf_counter()
    with (p/(name+'-build.log')).open('wb') as f:
        r = subprocess.run(cmd, env=env, stdout=f, stderr=subprocess.STDOUT)
    rec = dict(name=name, command=cmd, producer_sha256=sha(producer), exit=r.returncode, elapsed_seconds=time.perf_counter()-start)
    if r.returncode == 0:
        rec['output_sha256'] = sha(p/name)
    records.append(rec)
    (p/'builds.json').write_text(json.dumps(records, indent=2)+'\n')
    print(json.dumps(rec), flush=True)
    assert r.returncode == 0
(p/'fixpoint.json').write_text(json.dumps({'stage2_sha256':sha(p/'stage2'), 'stage3_sha256':sha(p/'stage3'), 'byte_identical':sha(p/'stage2')==sha(p/'stage3')},indent=2)+'\n')
assert sha(p/'stage2') == sha(p/'stage3')
with (p/'stage2-g.nm').open('wb') as f:
    subprocess.run(['nm','-n',str(p/'stage2-g')],stdout=f,check=True)
env['FERN_RC_CERTIFY_MEMO'] = str(p/'memo-profile')
warm = [str(p/'stage2'),'-O','-target','x86-64-linux','-emit','asm','-o',str(p/'checker-x86-64-linux.s'),str(subject),str(stdlib)]
with (p/'warm.log').open('wb') as f:
    r = subprocess.run(warm,env=env,stdout=f,stderr=subprocess.STDOUT)
assert r.returncode == 0
cmd = [str(root/'tools/usr/bin/time'),'-v',str(root/'tools/usr/bin/valgrind'),'--tool=callgrind','--callgrind-out-file='+str(p/'stage2.callgrind')] + warm[:7] + ['/dev/null'] + warm[8:]
start = time.perf_counter()
with (p/'stage2-profile.log').open('wb') as f:
    r = subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT)
record = dict(command=cmd, exit=r.returncode, valid_full_run=r.returncode==0, elapsed_seconds_under_callgrind=time.perf_counter()-start, source_head='c63e488b4ed21b7161417382b8e4f7f6f05a5da5', binary_sha256=sha(p/'stage2'), common_producer_sha256=sha(p/'stage1'),subject_sha256=sha(subject),subject_output_sha256=sha(p/'checker-x86-64-linux.s'),memo_warmed=True)
if r.returncode == 0:
    record['instructions'] = int(re.search(r'Collected\s*:\s*(\d+)',(p/'stage2-profile.log').read_text())[1])
(p/'stage2-profile.json').write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps(record),flush=True)
assert r.returncode == 0
