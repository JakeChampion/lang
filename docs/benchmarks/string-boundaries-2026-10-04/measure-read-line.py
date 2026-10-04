import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import statistics
import subprocess
import time

p = argparse.ArgumentParser()
p.add_argument('--before-root', type=Path, required=True)
p.add_argument('--after-root', type=Path, required=True)
p.add_argument('--build', action='store_true')
p.add_argument('--iterations', type=int, default=8)
a = p.parse_args()
out = Path('/tmp/fern-read-line-bench')
out.mkdir(exist_ok=True)
env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
workloads = {'short_ascii': b'a' * 63 + b'\n', 'long_ascii': b'a' * 8192 + b'\n',
             'long_unicode': 'aé中🙂'.encode() * 820 + b'\n'}
if a.build:
    src = out / 'read-line.fern'
    src.write_text('''import "std/i64";
function main(): i32 {
  let r = stdin();
  let total: i64 = 0;
  let count: i64 = 0;
  let longest: i64 = 0;
  let done: boolean = false;
  while (!done) {
    match (r.read_line()) {
      Some(line) => {
        total = total + line.len() as i64;
        count = count + 1 as i64;
        if (line.len() as i64 > longest) { longest = line.len() as i64; }
      },
      None => { done = true; }
    }
  }
  print(total.to_string() + " " + count.to_string() + " " + longest.to_string());
  return 0;
}
''')
    builds = {}
    for label, root in [('before', a.before_root), ('after', a.after_root)]:
        for checked in [False, True]:
            binary = out / (label + ('-census' if checked else ''))
            runenv = dict(env)
            if checked:
                runenv.update(FERN_SANITIZE='1', FERN_LEAKCHECK='1')
            subprocess.run([str(root/'build/bootstrap/stage2'), '-target', 'arm64-darwin', '-o', str(binary), str(src), str(root/'internal/stdlib')], env=runenv, check=True, capture_output=True, timeout=60)
            builds[binary.name] = {'bytes': binary.stat().st_size, 'sha256': hashlib.sha256(binary.read_bytes()).hexdigest()}
    print(json.dumps(builds, indent=2))
else:
    result = {'iterations': a.iterations, 'warmups': 2, 'samples': 7, 'workloads': {}}
    for name, line in workloads.items():
        data = line * a.iterations
        def run(label, checked=False):
            binary = out / (label + ('-census' if checked else ''))
            start = time.perf_counter_ns()
            r = subprocess.run([str(binary)], input=data, env=env, capture_output=True, timeout=60)
            elapsed = (time.perf_counter_ns() - start) / 1e6
            assert r.returncode == 0, (name, label, r.returncode, r.stderr)
            total, count, longest = map(int, r.stdout.split())
            assert total == len(data), (name, label, total, len(data))
            if label == 'after':
                assert (count, longest) == (a.iterations, len(line)), (name, count, longest)
            return r, elapsed, count, longest
        rows = {}
        for label in ['before', 'after']:
            r, _, count, longest = run(label, True)
            m = re.fullmatch(rb'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)\n', r.stderr)
            assert m and m[1] == m[2] and int(m[3]) == 0, (name, label, r.stderr)
            rows[label] = {'allocations': int(m[1]), 'returned_pieces': count, 'longest_piece': longest, 'samples_ms': []}
        for iteration in range(9):
            for label in (['before', 'after'] if iteration % 2 == 0 else ['after', 'before']):
                r, elapsed, _, _ = run(label)
                assert not r.stderr, r.stderr
                if iteration >= 2:
                    rows[label]['samples_ms'].append(elapsed)
        for row in rows.values():
            row['median_ms'] = statistics.median(row['samples_ms'])
        result['workloads'][name] = {'input_bytes': len(data), 'results': rows}
    print(json.dumps(result, indent=2))
