# The ownership checker compares field-read positions as integers

Issues #11534 and #8171.

The last-use analysis records reads of scalar fields and reads of nested
fields that hold a primitive value or an array of primitive values.
It used to format the receiver's line and column as decimal text on
both insertion and lookup. The two tables now store the line in the
upper 32 bits of an integer and the column in the lower 32 bits.
Every pair of signed 32-bit coordinates has its own key.

## Measurement

Both x86-64 compilers built themselves. Each then compiled and linked the
same copy of `compiler/checker.fern` and its dependencies from main at
`5b850f03cb31`. Callgrind ran with certification enabled and its on-disk
cache disabled. The allocation run used the settings from
`scripts/selfhost-alloc-bench`, with certification disabled.

| Checker compile | Main | Change | Reduction |
| --- | ---: | ---: | ---: |
| x86-64 instructions | 48,423,204,317 | 48,415,392,557 | 7,811,760 (0.016%) |
| Heap allocations | 55,168,618 | 55,142,816 | 25,802 (0.047%) |

Both allocation runs ended with zero live bytes. The checker binaries
matched byte for byte on x86-64 Linux, ARM64 Linux and wasm32 WASI.
Adding 65,536 spaces before a field read preserved the executable for
the scalar-field ownership fixture and the full E051 diagnostics for
two fixtures that borrow or copy a handle field before a move.
