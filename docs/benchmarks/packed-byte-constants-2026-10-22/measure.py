from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess
import sys
import time

if len(sys.argv) == 5:
    root, out, compiler, profile = [Path(p).resolve() for p in sys.argv[1:]]
else:
    out = Path('/workspace/scratch/lang-byte-literal-profile')
    root = Path('/workspace/lang-byte-literal-control')
    compiler = out / 'compiler-alloc'
    profile = out / 'alloc.pprof'
report = {
    'source_git': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip(),
    'compiler_sha256': hashlib.sha256(compiler.read_bytes()).hexdigest(),
    'producer_sha256': hashlib.sha256(Path('/workspace/scratch/lang-main-d669-control/stage3').read_bytes()).hexdigest(),
    'compiler_build_mode': 'alloc-pprof with -g',
    'rows': [],
}
env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
for name in ['small.bin', 'large.bin']:
    for kind in ['bytes', 'text']:
        label = name + '-' + kind
        source = out / (label + '.fern')
        assembly = out / (label + '.s')
        command = [str(compiler), '-embed', str(out / 'assets'), '-target', 'x86-64-linux', '-emit', 'asm', '-o', str(assembly), str(source), str(root / 'internal/stdlib')]
        stdout, stderr = out / (label + '.stdout'), out / (label + '.stderr')
        start = time.perf_counter()
        with stdout.open('wb') as output, stderr.open('wb') as error:
            proc = subprocess.Popen(command, cwd=root, env=env, stdout=output, stderr=error)
            _, status, usage = os.wait4(proc.pid, 0)
            proc.returncode = os.waitstatus_to_exitcode(status)
        wall = time.perf_counter() - start
        assert proc.returncode == 0, (label, proc.returncode, stderr.read_text())
        assert stdout.stat().st_size == 0 and stderr.stat().st_size == 0
        assert profile.stat().st_size > 0
        saved = out / (label + '.pprof')
        shutil.copyfile(profile, saved)
        row = {
            'label': label,
            'input_bytes': (out / 'assets' / name).stat().st_size,
            'command': command,
            'wall_seconds': wall,
            'user_seconds': usage.ru_utime,
            'system_seconds': usage.ru_stime,
            'max_rss_kib': usage.ru_maxrss,
            'minor_faults': usage.ru_minflt,
            'major_faults': usage.ru_majflt,
            'assembly_bytes': assembly.stat().st_size,
            'assembly_sha256': hashlib.sha256(assembly.read_bytes()).hexdigest(),
            'profile_sha256': hashlib.sha256(saved.read_bytes()).hexdigest(),
        }
        report['rows'].append(row)
        (out / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
        print({k: row[k] for k in ['label', 'wall_seconds', 'max_rss_kib', 'assembly_bytes']}, flush=True)
        for metric in ['alloc_space', 'alloc_objects']:
            with (out / (label + '-' + metric + '.txt')).open('w') as log:
                subprocess.run(['go', 'tool', 'pprof', '-top', '-nodecount=40', '-sample_index=' + metric, str(saved)], cwd=root, env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
