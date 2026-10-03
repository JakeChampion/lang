# Quoting arbitrary bytes as text

`gnu.quote_bytes` borrows a byte array and returns an ASCII diagnostic.
It preserves printable ASCII, escapes backslash and apostrophe, uses named
C escapes for their control characters, and emits three octal digits for
other bytes. Embedded NUL is preserved as an escape. The input remains
unchanged, including when another array retains it.

The implementation appends to a byte builder and extracts text only after
every emitted byte is known to be ASCII. It shares `c_escape_letter` with
the existing text quoting code. Existing quoting APIs are unchanged.

The earlier array-append implementation passed the byte corpus but timed
out after 60 seconds on a 65,536-byte primary-interpreter stress probe.
Instrumentation confirmed that input construction completed before the
timeout. The builder version completes that same probe in 10.201 seconds.
At 16,384 bytes, the diagnostic runs take 9.095 seconds before and 4.378
seconds after. These are single observations including input construction
and interpreter startup, not throughput benchmarks.

The final stage-2 compiler from parent `31a5dac13` passes the all-byte,
empty-input, mixed-escape and retained-alias fixture on Darwin, core WASM
and the primary interpreter. The scaled pipeline first passes 256 bytes,
then 65,536 bytes with only the size parameter changed. The larger output
is exactly 186,115 bytes including its printed newline. Both compiled
targets balance all allocations and frees.

| Probe | Previous Darwin allocations | Builder Darwin | Previous WASM allocations | Builder WASM |
| --- | ---: | ---: | ---: | ---: |
| Complete byte/alias fixture | 36 | 34 | 55 | 35 |
| 256-byte quoting program | 13 | 10 | 19 | 12 |

The 65,536-byte builder program uses 18 allocations on Darwin and 20 on
WASM, with no live bytes at exit.

Both native comparison programs occupy 49,713 file bytes. Builder code
shrinks from 14,812 to 14,580 bytes, and unwind data from 2,980 to 2,876.
Core WASM grows from 9,354 to 9,481 bytes as it uses the builder helpers.
Both native programs emit the same 730-byte all-byte diagnostic. The same
parent compiler builds both versions; no compiler source or size baseline
changes in this slice.

Final Linux checks pass on the builder implementation: interpreter pilot
0.462 seconds, Go target matrix 1.364 seconds, and primary target/CLI checks
27.206 seconds. The full unit suite and every lint gate also pass.
