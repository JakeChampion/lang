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
p.add_argument('--root', type=Path, required=True)
p.add_argument('--before', type=Path, required=True)
p.add_argument('--after', type=Path, required=True)
p.add_argument('--build', action='store_true')
p.add_argument('--iterations', type=int, default=128)
a = p.parse_args()
out = Path('/tmp/fern-builder-text-bench')
out.mkdir(exist_ok=True)
env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
workloads = {
    'short_ascii': (b'abcdefghijklmnopqrstuvwxyz012345', b'abcdefghijklmnopqrstuvwxyz012345'),
    'long_ascii': (b'abcdefgh' * 1024, b'abcdefgh' * 1024),
    'scalars': ('¢€𐐀'.encode() * 512, '¢€𐐀'.encode() * 512),
    'malformed': (b'a\xe2\x82b\xff' * 512, 'a�b�'.encode() * 512),
}
if a.build:
    builds = {}
    for name, (data, expected) in workloads.items():
        source = '''import "core/int";
import "std/i64";
function main(): i32 {
  let argv: string[] = args();
  let emit: boolean = argv.len() > 2;
  let count: i32 = 0;
  match (int.parse_int_radix(argv[1], 10)) { Some(n) => { count = n; }, None => { return 2; } }
  let bytes: u8[] = [DATA];
  let h: usize = buf_new(bytes.len());
  let sum: i64 = 0;
  let i: i32 = 0;
  while (i < count) {
    buf_push_bytes_range(h, bytes, 0, bytes.len());
    let text: string = buf_take(h);
    if (emit) { write(text); }
    sum = sum + text.len() as i64 + text[0] as i64 + text[text.len() - 1] as i64;
    i = i + 1;
  }
  buf_free(h);
  print(sum.to_string());
  return 0;
}
'''.replace('DATA', ','.join(f'{b} as u8' for b in data))
        src = out / (name + '.fern')
        src.write_text(source)
        for label, compiler in [('before', a.before), ('after', a.after)]:
            for checked in [False, True]:
                binary = out / (name + '-' + label + ('-census' if checked else ''))
                runenv = dict(env)
                if checked:
                    runenv.update(FERN_SANITIZE='1', FERN_LEAKCHECK='1')
                subprocess.run([str(compiler), '-target', 'arm64-darwin', '-o', str(binary), str(src), str(a.root/'internal/stdlib')], env=runenv, check=True, capture_output=True, timeout=60)
                if not checked:
                    asm = binary.with_suffix('.s')
                    subprocess.run([str(compiler), '-target', 'arm64-darwin', '-emit', 'asm', '-o', str(asm), str(src), str(a.root/'internal/stdlib')], env=env, check=True, capture_output=True, timeout=60)
                    builds[binary.name] = {'bytes': binary.stat().st_size, 'sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
                                         'sections': subprocess.check_output(['/usr/bin/size', '-m', str(binary)], text=True)}
    print(json.dumps(builds, indent=2))
else:
    result = {'iterations': a.iterations, 'warmups': 2, 'samples': 7, 'workloads': {}}
    for name, (data, repaired) in workloads.items():
        def run(label, checked=False, timed=False):
            binary = out / (name + '-' + label + ('-census' if checked else ''))
            cmd = [str(binary), str(a.iterations)]
            if timed:
                cmd = ['/usr/bin/time', '-l'] + cmd
            started = time.perf_counter_ns()
            r = subprocess.run(cmd, env=env, capture_output=True, timeout=60)
            ms = (time.perf_counter_ns() - started) / 1e6
            expected = data if label == 'before' else repaired
            want = f'{(len(expected) + expected[0] + expected[-1]) * a.iterations}\n'.encode()
            assert r.returncode == 0 and r.stdout == want, (name, label, r.returncode, r.stdout, want, r.stderr)
            return r, ms
        rows = {}
        for label in ['before', 'after']:
            expected = data if label == 'before' else repaired
            binary = out / (name + '-' + label)
            exact = subprocess.run([str(binary), '1', 'check'], env=env, capture_output=True, timeout=60)
            checksum = f'{len(expected) + expected[0] + expected[-1]}\n'.encode()
            assert exact.returncode == 0 and not exact.stderr and exact.stdout == expected + checksum, (name, label, exact)
            r, _ = run(label, checked=True)
            m = re.fullmatch(rb'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)\n', r.stderr)
            assert m and m[1] == m[2] and int(m[3]) == 0, (name, label, r.stderr)
            rows[label] = {'allocations': int(m[1]), 'samples_ms': []}
        for iteration in range(9):
            order = ['before', 'after'] if iteration % 2 == 0 else ['after', 'before']
            for label in order:
                r, elapsed = run(label)
                assert not r.stderr, r.stderr
                if iteration >= 2:
                    rows[label]['samples_ms'].append(elapsed)
        for label in ['before', 'after']:
            r, _ = run(label, timed=True)
            rss = re.search(rb'(\d+)\s+maximum resident set size', r.stderr)
            assert rss, r.stderr
            rows[label]['peak_rss_bytes'] = int(rss[1])
            rows[label]['median_ms'] = statistics.median(rows[label]['samples_ms'])
        result['workloads'][name] = {'input_bytes_per_take': len(data), 'output_bytes_before': len(data), 'output_bytes_after': len(repaired), 'results': rows}
    print(json.dumps(result, indent=2))
