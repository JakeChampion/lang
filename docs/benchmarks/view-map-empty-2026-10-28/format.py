from pathlib import Path
import hashlib
import json
import subprocess
import time

p = Path(__file__).parent
repo = Path('/workspace/lang-perf-4412')
files = subprocess.check_output(['git', 'ls-files', '*.fern', '*.fern.md'], cwd=repo, text=True).splitlines()
files = [name for name in files if not name.startswith('conformance/') and '/testdata/' not in name]
command = [str(repo / 'bin/fern'), '-fmt', '-d'] + files
started = time.perf_counter()
with (p / 'formatter.log').open('wb') as out:
    result = subprocess.run(command, cwd=repo, stdout=out, stderr=subprocess.STDOUT)
record = dict(command=['bin/fern', '-fmt', '-d', 'all tracked inputs with Makefile filters'], exit=result.returncode, seconds=time.perf_counter() - started, inputs=len(files), source_sha256=hashlib.sha256((repo / 'compiler/asm_ir.fern').read_bytes()).hexdigest(), formatter_sha256=hashlib.sha256((repo / 'bin/fern').read_bytes()).hexdigest())
(p / 'formatter.json').write_text(json.dumps(record, indent=2) + '\n')
print(json.dumps(record))
assert result.returncode == 0
