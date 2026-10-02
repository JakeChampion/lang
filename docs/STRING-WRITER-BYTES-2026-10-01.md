# Raw Writer output

`Writer.write_bytes(u8[]): Option[IoError]` completes short writes or
returns an error. Zero progress is an error for a nonempty remainder.
`Writer.write_some_bytes(u8[]): Result[i64, IoError]` returns the count
from one backend write, which may be zero. Both methods borrow their
input and preserve arbitrary bytes without constructing a string.
Empty writes still observe host errors; closed handles fail.

The implementation covers the bootstrap interpreter, native and SSA
backends, WASM Preview 1 and Preview 2, and the primary compiler's native
and WASM backends. Component output includes the prerequisite that chunks
host writes, propagates failures and releases scratch and error resources.
The WASM success box also writes the correct payloadless `None` tag.

The shared fixture covers all byte values, aliases, empty writes, short
write counts, closed handles, and the success value stored in a map.
Interpreter host tests cover short writes, interruption and zero progress.
Darwin, x86-64 Linux, ARM64 Linux and WASM tests pass, with strict semantic
IR and balanced ownership checks on primary compiled targets. Actual
components pass on stdout and stderr with both lowering modes. The IR
registry and payloadless-result regression pass. The first full gate hit
the host's system-wide file-handle limit in the printer and source tests.
The full unit suite and `make lint-all` pass on retry with reduced host
concurrency.

The pinned Darwin bootstrap passes compiler and `tr` smoke tests.
Stage2 and stage3 match at 14,378,593 bytes, SHA-256
`cec46a1a1718f257f34224a00f7e0c95add5249e3a70007af843d0f7d3bd8886`.
The primary interpreter's raw host bridge remains a later change that
requires a compatible bootstrap seed. This slice does not claim that
interpreter parity or the full UTF-8 invariant is complete.

## Measurement

The same primary compiler builds two programs from the same owned byte
input. The baseline copies each block into a builder, extracts a string,
then calls the text writer. The new version writes the array directly.
ASCII input keeps the baseline within its text contract.

A four-round pilot passed before scaling only the round count to 64,
with 1 MiB per round. Both versions write to a temporary regular file;
every output byte is checked outside the timed interval. Two warmups
precede seven alternating samples. Sanitizer, allocation census and RC
debug instrumentation are absent. Other validation jobs were active.

| Version | Median | Range |
| --- | ---: | ---: |
| Builder and text writer | 22.205 ms | 19.921-34.545 ms |
| Direct raw writer | 10.536 ms | 9.872-19.959 ms |

The median falls in this workload, but the ranges overlap. This is not
a general throughput claim.

Native text decreases from 24,620 to 22,564 bytes. Object text decreases
by 2,052 bytes: the new raw write helper adds 348, removing the text write
helper saves 292, removing builder helpers saves 1,968, and the caller
saves 140. Native layout accounts for the remaining four bytes.
Constant, data and BSS sizes are unchanged. No size baseline changes.
