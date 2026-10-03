# Quoting arbitrary bytes as text

`gnu.quote_bytes` borrows a byte array and returns an ASCII diagnostic.
It preserves printable ASCII, escapes backslash and apostrophe, uses named
C escapes for their control characters, and emits three octal digits for
other bytes. Embedded NUL is preserved as an escape. The input remains
unchanged, including when another array retains it.

The implementation appends to a byte builder and extracts text only after
every emitted byte is known to be ASCII. It shares `c_escape_letter` with
the existing text quoting code. Existing quoting APIs are unchanged.

During preparation at `31a5dac13`, the earlier array-append implementation passed the byte corpus but timed
out after 60 seconds on a 65,536-byte primary-interpreter stress probe.
Instrumentation confirmed that input construction completed before the
timeout. The builder version completes that same probe in 10.201 seconds.
At 16,384 bytes, the diagnostic runs take 9.095 seconds before and 4.378
seconds after. These are single observations including input construction
and interpreter startup, not throughput benchmarks.

The final stage-2 compiler from publication parent `96f7b2b95` passes the all-byte,
empty-input, mixed-escape and retained-alias fixture on Darwin, core WASM
and the primary interpreter. The scaled pipeline first passes 256 bytes,
then 65,536 bytes with only the size parameter changed. The larger output
is exactly 186,115 bytes including its printed newline. Both compiled
targets balance all allocations and frees.

The complete byte/alias fixture uses 34 allocations on Darwin and 35 on
core WASM. The 65,536-byte builder program uses 18 allocations on Darwin
and 20 on WASM, with no live bytes at exit.

Both native comparison programs occupy 49,713 file bytes. Builder code
shrinks from 17,268 to 15,308 bytes, and unwind data from 3,284 to 3,180.
Core WASM grows from 9,932 to 10,023 bytes as it uses the builder helpers.
Both native programs emit the same 730-byte all-byte diagnostic. The same
parent compiler builds both versions, replacing only the quoting function
in the current GNU helper module; no compiler source or size baseline
changes in this slice.

Final Linux checks pass on the builder implementation: interpreter pilot
0.519 seconds, Go target matrix 1.395 seconds, and primary target/CLI checks
27.966 seconds. The full unit suite and every lint gate also pass. Darwin's
primary native and interpreter gate passes in 36.877 seconds.
The Go compiler's Darwin fixture also passes in 2.539 seconds.
