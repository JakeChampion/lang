#!/usr/bin/env python3
"""Compare file decoding with the independent double-precision FFT path."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
ULC = Path(os.environ.get('ULC', ROOT / 'build' / 'ulc'))
REFERENCE = Path(os.environ.get('ULC_REFERENCE64', ROOT / 'build' / 'reference64'))
spec = importlib.util.spec_from_file_location('ulc_benchmark', ROOT / 'bench' / 'run.py')
benchmark = importlib.util.module_from_spec(spec)
spec.loader.exec_module(benchmark)


def run(*args):
    subprocess.run([str(x) for x in args], check=True, stdout=subprocess.PIPE,
                   stderr=subprocess.PIPE, timeout=120)


def main():
    maximum = 0.0
    with tempfile.TemporaryDirectory(prefix='ulc-accuracy-') as directory:
        root = Path(directory)
        for size in (256, 32768):
            for channels in (1, 2, 3):
                for kind in ('noise', 'transients'):
                    source, stream = root / 'input.wav', root / 'input.ulc'
                    benchmark.fixture(source, kind, 1, channels)
                    run(ULC, 'encode', source, stream, 75, size)
                    actual, expected = root / 'actual.wav', root / 'expected.wav'
                    run(ULC, 'decode', stream, actual, 'FLOAT32', 1)
                    run(REFERENCE, stream, expected)
                    a, b = benchmark.samples(actual), benchmark.samples(expected)
                    assert len(a) == len(b)
                    error = max((abs(x - y) for x, y in zip(a, b)), default=0)
                    assert error < 0.00001, (size, channels, kind, error)
                    maximum = max(maximum, error)
    print(f'ok - file decoding matches float64 FFT at both block limits (max error {maximum:.3g})')


if __name__ == '__main__':
    main()
