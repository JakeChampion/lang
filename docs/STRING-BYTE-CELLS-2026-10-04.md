# Shared byte buffers and raw time formats

The final producer audit for #5714 found two consumers that still needed
raw storage after the string boundaries were tightened: private HTTP stream
buffers and coreutils time formats.

## Shared HTTP buffers

`Cell[u8[]]` stores an owned byte array. `get` returns a value snapshot, and
`set` preserves any existing aliases before releasing the replaced array.
Self-assignment follows the same rule. The cell and its last value are
reclaimed on final drop, including when the cell is held in a container.
Borrowed views and general composite cell elements remain unsupported.

The primary compiler already lowers array ownership through typed IR. Its
checker now admits byte-array cells, and the Go bootstrap compiler applies
the same retain, replacement and drop rules. To remain buildable by the
pinned compiler, the primary interpreter encodes its internal byte-cell
values as ASCII hex. Compiled application cells store arrays directly.

The HTTP stream implementation uses these cells for pending input, held
chunk-decoder data and pipelined leftovers. Tests exercise every byte value
in content-length and chunked requests, partial initial reads, and a second
binary request on the same connection. The byte API returns the exact body;
the text API continues to reject malformed UTF-8.

## Raw time formats

PR #11493 exposed a regression: `date -d '2024-06-15'` with the format bytes
`ff 25 59 fe` returned replacement characters around `2024`, while GNU
preserves `ff` and `fe`. The shared formatter had used a text builder result
for OS-supplied formats.

Compiled formats now retain one owned byte array and represent literals
with offsets into it. This also avoids constructing partial strings when an
unknown conversion ends inside a UTF-8 scalar. `format` repairs invalid text
after joining the pieces; `format_bytes` preserves exact bytes. `date`, `du`,
`ls` and `pr` use the byte result for CLI output. Checked writes retain their
existing failure status. Long listing prefixes also free their drained
builder.

## Validation

The byte-cell fixture covers all byte values, empty arrays, snapshots,
self-assignment, copy-on-write updates, aliases through closures and
containers, repeated overwrites and balanced allocation censuses. The Go
and primary interpreter paths, Linux x86-64 and ARM64, core WebAssembly,
components and primary per-module compilation passed.

The formatter fixture verifies raw and repaired output, retained source
ownership, repeated rendering, unknown Unicode directives and all 256 byte
values. It passed on the Go interpreter, both Linux native targets and both
Go WebAssembly formats, plus the primary compiler's native and core WASM
targets. Native and core WASM allocation censuses balance.

All four full GNU 9.12 parity suites passed on Linux, as did the primary
Date byte corpus on x86-64, ARM64 and core WASM. The closing UTF-8 property
also passed on the Go interpreter and the primary compiler's three targets.
The integrated full unit suite and `make lint-all` passed. Its HTTP static
and retired instruction counts reproduce the values in the
[performance attribution](STRING-BUILDER-PERF-2026-10-04.md).
Linux and Darwin bootstrap fixed points, reproduced native checks and native
benchmarks are recorded in the [boundary report](STRING-VALIDITY-BOUNDARIES-2026-10-04.md).
The final main integration and publication gates remain open for #5714.
