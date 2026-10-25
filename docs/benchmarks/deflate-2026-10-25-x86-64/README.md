# std/deflate against libdeflate on x86-64, 2026-10-25

This report measures the decoder work of 2026-10-25 on x86-64. It uses the
method of `deflate-2026-10-23/`, with a different host, corpus and libdeflate
release. Thus its numbers do not compare directly with that report.

## Setup

- Host: Intel Xeon at 2.1 GHz, 4 cores, Linux, target `x86-64-linux`. Other
  jobs share the host, so a time can move by 5% between runs.
- C: libdeflate 1.26 from the PyPI `deflate` 0.9.0 source package, built with
  CMake `Release` and the harness with `cc -O2`.
- Fern: `scripts/deflate-bench.d/fbench.fern` built by `bin/fern -O`.
- Input: eight local files, 68.8 MB in total. The network policy of the host
  blocked the Silesia corpus. The text files are Vim help text (`text`), Go
  source (`source`), XML (`xml`) and a Unicode collation table (`table`). The
  binary files are a Go executable (`exe`) and shared libraries (`lib`).
  `mixed` holds a book and a key file, and `incompressible` holds compressed
  images.
- Each time is the best of 3 runs. Each slowdown below is the median over the
  eight files.
- Each decoder decodes the streams of the other encoder. Every decode is
  compared with the original file, and all of them match.

`results-before.csv` holds the rows for #13154. `results-after.csv` holds the
rows after the compiler and decoder changes. Run
`python3 scripts/deflate-bench.d/deflate_bench_rows.py` on a file to get its
table.

## Results

| | before | after |
| --- | ---: | ---: |
| Decompression, Fern's own stream against the nearest libdeflate level | 3.76x | 2.56x |
| Decompression, one libdeflate level-6 stream | 4.64x | 2.82x |
| Compression against the nearest libdeflate level | 4.77x | 5.00x |

The compression row did not change outside the run-to-run spread: no change
touched the encoder.

| File | Fern decompress MB/s, before | after | libdeflate MB/s |
| --- | ---: | ---: | ---: |
| exe | 142.9 | 197.4 | 548.5 |
| lib | 138.2 | 207.1 | 445.2 |
| mixed | 117.6 | 236.7 | 500.3 |
| source | 480.6 | 715.5 | 1795.9 |
| table | 346.9 | 557.6 | 1456.6 |
| text | 216.4 | 255.4 | 693.7 |
| xml | 310.3 | 612.3 | 1676.8 |
| incompressible | 152.7 | 251.2 | 378.9 |

## What the decoder still pays

The fast loop has no bounds checks and no per-push count test. On every
literal it still writes the output array's length to memory and reads it back
on the next literal. The next store and load wait for each other, which is a
chain of about five cycles. libdeflate keeps its output pointer in a register.
The next step is to keep a sole array's length in a register across a loop.
