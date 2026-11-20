"""Compare two host timestamp libraries on the same mocked AAudio input."""
import ctypes
import hashlib
import json
import os
import pathlib
import statistics
import subprocess
import sys
import tempfile


def digest(path):
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()


def capture(library):
    with tempfile.TemporaryFile() as capture:
        saved = os.dup(1)
        try:
            os.dup2(capture.fileno(), 1)
            result = library.main()
        finally:
            os.dup2(saved, 1)
            os.close(saved)
        assert result == 0, result
        capture.seek(0)
        rows = [json.loads(line) for line in capture.read().decode().splitlines()]
    return rows


def invoke(library):
    rows = capture(library)
    assert len(rows) == 12
    assert {(r['channels'], r['unavailable'], r['calls']) for r in rows} == {
        (c, u, n) for c in (0, 1, 2) for u in (False, True) for n in (100, 100000)
    }
    return rows


def worker(base, candidate, process):
    libraries = {'base': ctypes.CDLL(base), 'candidate': ctypes.CDLL(candidate)}
    records = []
    for trial in range(10):
        order = ['base', 'candidate'] if (trial + process) % 2 == 0 else ['candidate', 'base']
        for position, variant in enumerate(order):
            for row in invoke(libraries[variant]):
                expected = row['calls'] if variant == 'base' else 0
                assert row['allocations'] == expected, (variant, row)
                assert row['wall_ns'] > 0
                records.append(dict(row, variant=variant, process=process, trial=trial, order=position))
    return records


def main():
    if sys.argv[1] == '--worker':
        print(json.dumps(worker(sys.argv[2], sys.argv[3], int(sys.argv[4]))))
        return
    base, candidate, destination = map(pathlib.Path, sys.argv[1:])
    records = []
    for process in range(3):
        run = subprocess.run([sys.executable, __file__, '--worker', str(base.resolve()),
                              str(candidate.resolve()), str(process)], check=True, capture_output=True, text=True)
        records.extend(json.loads(run.stdout))
    summary = []
    for channels in (0, 1, 2):
        for unavailable in (False, True):
            row = {'channels': channels, 'unavailable': unavailable}
            for variant in ('base', 'candidate'):
                trials = [r for r in records if r['channels'] == channels and r['unavailable'] == unavailable
                          and r['variant'] == variant and r['calls'] == 100000]
                assert len(trials) == 30
                row[variant] = {'median_ns_per_call': statistics.median(r['wall_ns'] / r['calls'] for r in trials),
                                'allocations_per_call': trials[0]['allocations'] / 100000}
            summary.append(row)
    report = {'schema': 1, 'scope': 'Host mocks only. No device decode, playback, or physical lip-sync measurement.',
              'processes': 3, 'pairs_per_process': 10, 'warmup_calls_per_case': 100,
              'measured_calls_per_case': 100000, 'sample_rate': 48000,
              'library_sha256': {'base': digest(base), 'candidate': digest(candidate)},
              'source_sha256': {p: digest(p) for p in (
                  'bench/audio/android_timestamp.fern', 'scripts/audio-reference/bench-timestamp.py',
                  'scripts/audio-reference/aaudio-mock.c', 'scripts/video-reference/mediandk-mock.c',
                  'scripts/video-reference/codec-jni-mock.c')},
              'summary': summary, 'records': records}
    destination.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(summary, indent=2))


if __name__ == '__main__':
    main()
