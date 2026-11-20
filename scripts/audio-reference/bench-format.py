"""Compare two MediaCodec format readers with common host mocks."""
import ctypes
import hashlib
import json
import os
import pathlib
import runpy
import statistics
import subprocess
import sys

capture = runpy.run_path('scripts/audio-reference/bench-timestamp.py')['capture']


def invoke(library, per_query):
    rows = capture(library)
    assert len(rows) == 4
    assert {(r['profile'], r['queries']) for r in rows} == {
        (p, n) for p in (0, 1) for n in (100, 100000)
    }
    assert all(r['allocations'] == per_query * r['queries'] and r['wall_ns'] > 0 for r in rows), rows
    return rows


def worker(base, candidate, kind, process):
    libraries = {variant: ctypes.CDLL(str(root / (kind + '.so')))
                 for variant, root in [('base', base), ('candidate', candidate)]}
    records = []
    for trial in range(10):
        order = ['base', 'candidate'] if (trial + process) % 2 == 0 else ['candidate', 'base']
        for position, variant in enumerate(order):
            per_query = 2 if variant == 'candidate' else (14 if kind == 'video' else 6)
            rows = invoke(libraries[variant], per_query)
            for row in rows:
                assert row['allocations'] == per_query * row['queries'], (variant, kind, row)
                assert row['wall_ns'] > 0
                records.append(dict(row, kind=kind, variant=variant, process=process, trial=trial, order=position))
    return records


def digest(path):
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()


def main():
    if sys.argv[1] == '--worker':
        print(json.dumps(worker(pathlib.Path(sys.argv[2]), pathlib.Path(sys.argv[3]), sys.argv[4], int(sys.argv[5]))))
        return
    base, candidate, destination = [pathlib.Path(p).resolve() for p in sys.argv[1:]]
    records = []
    for kind in ('video', 'audio'):
        for process in range(3):
            env = dict(os.environ, LD_LIBRARY_PATH=str(candidate / kind))
            run = subprocess.run([sys.executable, __file__, '--worker', str(base), str(candidate), kind, str(process)],
                                 env=env, check=True, capture_output=True, text=True)
            records.extend(json.loads(run.stdout))
    summary = []
    for kind in ('video', 'audio'):
        for profile in (0, 1):
            row = {'kind': kind, 'profile': profile}
            for variant in ('base', 'candidate'):
                trials = [r for r in records if r['kind'] == kind and r['profile'] == profile
                          and r['variant'] == variant and r['queries'] == 100000]
                assert len(trials) == 30
                row[variant] = {'median_ns_per_query': statistics.median(r['wall_ns'] / r['queries'] for r in trials),
                                'fern_allocations_per_query': trials[0]['allocations'] / 100000}
            summary.append(row)
    sources = ['bench/audio/android_video_format.fern', 'bench/audio/android_audio_format.fern',
               'scripts/audio-reference/bench-format.py', 'scripts/audio-reference/bench-timestamp.py',
               'scripts/video-reference/mediandk-mock.c', 'scripts/audio-reference/mediacodec-mock.c',
               'scripts/video-reference/codec-jni-mock.c']
    report = {'schema': 1, 'scope': 'Host format mocks only. Fern heap counters exclude platform format allocations.',
              'processes_per_kind': 3, 'pairs_per_process': 10, 'warmup_queries_per_profile': 100,
              'measured_queries_per_profile': 100000, 'summary': summary, 'records': records,
              'library_sha256': {v + '/' + k: digest(root / (k + '.so')) for v, root in
                                 [('base', base), ('candidate', candidate)] for k in ('video', 'audio')},
              'mock_sha256': {k + '/' + n: digest(candidate / k / n) for k in ('video', 'audio')
                              for n in ('libmediandk.so', 'libandroid.so')},
              'source_sha256': {p: digest(p) for p in sources}}
    destination.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(summary, indent=2))


if __name__ == '__main__':
    main()
