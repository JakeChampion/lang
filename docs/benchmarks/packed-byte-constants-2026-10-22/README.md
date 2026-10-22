# Packed byte constants: 2026-10-22

The initial profile predates the implementation. Its source is `c84f7c6a5`.
The patch starts at `12625f42b`. The two commits between those points change
declaration diagnostics. A second control uses that exact base.

The final source
is `95a86695a`. The later changes add tests and this record.

Each profiling compiler uses `alloc-pprof` and `-g`. The same frozen stage-three
producer builds both compilers:
`3be44999ff7041c0a240210726a73b854f5d3b9d419e1f34d222d3d6442df8d8`.
The inputs contain 100,000 and 1,000,000 valid UTF-8 bytes. Each source returns
the length modulo seven through a function with `@noinline`.

`measure.py` records each command, wall time, child CPU time, peak RSS, faults,
output hash, and profile hash. Each row is one compile. The final run starts
after the validation processes exit. These times describe this machine and run.

The exact-base control and final measurements are below. RSS is in KiB and wall
time is in seconds. `control-base12625/results.json` and `final/results.json`
contain the full measurements.

| Input | Form | Control RSS | Final RSS | Control wall | Final wall |
| --- | --- | ---: | ---: | ---: | ---: |
| 100 KB | bytes | 184,828 | 29,824 | 1.481 | 0.126 |
| 100 KB | text | 29,824 | 29,824 | 0.121 | 0.131 |
| 1 MB | bytes | 1,623,420 | 46,352 | 7.496 | 0.186 |
| 1 MB | text | 29,824 | 29,824 | 0.139 | 0.120 |

All four final assembly files match both controls byte for byte.
`assembly-parity.json` records their hashes.

The initial profile retained 8,192 samples and dropped 400,759. Its sampled
allocation weight was about 1,533 MB. `semlower.target_substitution` accounted for
72.79% cumulatively. The final 1 MB byte profile retained 1,282 samples and dropped
none, with about 6.54 MB of allocation weight. The profiles include overlapping
call stacks, so their cumulative percentages are not additive.

The `large-executables` directory records full executable builds with a different
binary input: bytes 0 through 255 in a cycle. Both the 1 MB and 10 MB programs
compile and run on x86-64 Linux and Wasm. The 10 MB builds use 494,360 and
506,008 KiB peak RSS, respectively. They complete in 1.805 and 1.600 seconds.
Each program checks its length and bytes at three positions.

The functional regression tests check every byte value and the ownership
behavior separately.

## Reproduce

The assets and profiles use gzip. Decompress the assets into an `assets`
directory beside the source fixtures. Decompress each `.pprof.gz` before use
with `go tool pprof`. The assembly control files are compressed copies of the
original outputs. Their decompressed hashes match `baseline/results.json`.

`commands.json` records the build and validation commands. Each results file
records the commands for its individual compiles. The paths name the local
immutable source trees and binaries. Replace those roots for another machine.
`measure.py` also names the frozen producer for its hash record.

## Validation

- `validation/broad.log`: the constant-data, packed-byte, semantic, FIP, C-call,
  registry, census, and complexity suites. Only the complexity gate failed.
- `validation/refactor-targeted.log`: the complexity gate and focused functional
  tests pass after the verifier and coercion checks were split into helpers.
- `validation/coercion-differential-final.log`: the graph checks and selected
  array, tuple, and literal checker comparisons pass.

- `validation/bootstrap.json`: stages two and three are identical at 14,159,904
  bytes, SHA-256 `a8de45640ae581d99560982db934d4c51a74b897cd923c5158fc762f3dda72e2`.
- `validation/driver-gate.log`: all eleven strict driver-size rows pass without
  a baseline change. The driver builds took 184.042 seconds.
