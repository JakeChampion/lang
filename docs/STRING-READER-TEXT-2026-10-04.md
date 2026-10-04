# Reader text validation

`Reader.read_chunk(n)` now validates the bytes returned by one physical
read. Malformed or incomplete UTF-8 returns `InvalidUtf8("")` and consumes
that read. It does not read ahead or retain an unfinished scalar. Callers
that need arbitrary chunks use `read_chunk_bytes`; buffered text callers
use `LineReader`.

Negative sizes return EINVAL before allocation or reading. Empty successful
reads, I/O errors and closed handles retain their existing distinctions.
The Go interpreter validates bytes returned alongside EOF or an error,
matching the normal host-reader rule that those bytes take precedence.

The native and WASM backends reuse the existing UTF-8 validator. WASM also
releases rejected scratch and compacts short reads. The self-host interpreter
reads raw bytes and calls `utf8.from_bytes` explicitly, so the installed
stage 1 has this contract even when its compiler pin has the older runtime.

## Validation

The source is based on consumer `79df9e5ef`. An independent Go UTF-8 oracle
covers valid scalar widths, NUL, invalid leads, overlongs, surrogates,
truncation, split scalars, continued raw reads, retained snapshots, short
reads, negative and zero sizes, EOF and closed handles. Host-reader tests
also cover partial data with errors and interruption.

- Go interpreter, Linux ARM64/x86-64, WASM core and component targets pass.
- Primary pin-built interpreter, current-backend interpreter, Linux native,
  WASM core/component and separate-module links pass in 43.759 seconds.
- Full Linux units and `make lint-all` pass.
- Fresh bootstrap reaches stage 2 == stage 3 at 13,066,688 bytes, SHA-256
  `def1f92dfb208628935491e401aab6848f588136c5cea61d313fc4f79307475a`.
  Stage 1 differs. The reproduced-primary text/raw/link regression passes
  in 44.621 seconds.
- Darwin Go targets pass in 2.698 seconds; primary targets, both interpreter
  builds and the component pass in 18.637 seconds.

Native and WASM core tests check balanced allocation censuses. Components
run the same functional cases but do not emit an exit-time census.

## Measured cost

The before compiler is the reproduced consumer at `79df9e5ef`, SHA-256
`e0fe1c2bed31f5b23306bf6deabbae695b37551fe19ae292041107a9d56f7b0a`.
The after compiler is the reproduced Reader candidate above. Both compile
the same programs against the candidate stdlib. Runs use native ARM64 Linux
inside Docker with two CPUs, not QEMU. These are local measurements, not
cross-platform throughput claims.

Each program consumes a regular file through stdin in fixed-size chunks,
sums each returned length and its endpoint bytes, and checks the result
against a Python oracle. A 128-chunk pilot verifies the whole measurement
path; the larger run changes only the count to 8,192 chunks. Each side has
two warmups and seven samples, alternating execution order. Timings include
process startup. The table reports medians in milliseconds.

| Chunk | Input bytes | Before | After | Allocations, each side | Executable bytes, each side |
|---|---:|---:|---:|---:|---:|
| 64-byte ASCII | 524,288 | 3.904 | 3.876 | 16,388 | 69,888 |
| 4,096-byte ASCII | 33,554,432 | 13.225 | 30.494 | 16,388 | 69,888 |
| 4,095-byte mixed Unicode | 33,546,240 | 12.947 | 35.837 | 16,388 | 69,888 |

Validation adds a scan, visible on the larger chunks. The small-chunk samples
overlap; they do not establish a speedup. Allocation counts and linked sizes
are unchanged, and both sides free every counted allocation with zero live
bytes. The Unicode chunk is 409 copies of `aé中🙂` followed by `abcde`.
ASCII chunks repeat `abcdefgh`. This benchmark measures valid text reads;
it makes no performance claim for malformed data or the raw byte API.

The [measurement script](benchmarks/reader-text-2026-10-04/measure.py),
[build identities](benchmarks/reader-text-2026-10-04/builds.json),
[pilot](benchmarks/reader-text-2026-10-04/pilot.json) and
[full samples](benchmarks/reader-text-2026-10-04/full.json) preserve the run.
On native ARM64 Linux, pass the source root, both reproduced compilers and
an output directory to the script. Run with `--build`, then `--chunks 128`,
then `--chunks 8192`, using `uv run --no-project python` each time.
