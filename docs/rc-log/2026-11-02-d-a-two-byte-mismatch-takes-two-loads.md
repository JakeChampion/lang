# 2026-11-02 — a two-byte mismatch takes two loads

`emit_mismatch_x86`, `emit_mismatch_arm64`. A `sort` lead in #8278.

## The shape

`sort` of 500,000 words compares 4,131,365 pairs of lines. Each compare
calls `__mismatch_bytes` through `cmp_bytes`. The words are 3 to 5 bytes
long, so 45% of the calls compare fewer than 4 bytes. The kernel had arms
for 16, 8 and 4 bytes, and sent a shorter count to the byte loop: 7
instructions a byte, 23 for a 3-byte word.

## The rule

A count of 2 or 3 takes two 16-bit loads, at the start and at the end, as
the 4-byte and 8-byte arms take theirs. The loads overlap for a count of 3.
The first differing byte comes from the lowest set bit of the exclusive
or, as in the wider arms. A count of 1 still takes the byte loop, for one
round.

## Measured

x86-64, `-O`, instructions from callgrind, `setarch x86_64 -R`.

| | before | after |
|---|--:|--:|
| `sort` of 500,000 words | 1,189,760,185 | 1,161,754,890 |

The compiler itself does not reach the arm. The stage-2 compiler is
15,761,744 bytes with and without it. Its compile of `lexer.fern` moves by
12,619 instructions out of 1,760,441,980.

The comparison path still costs about 150 instructions a compare. `compare`
and `cmp_bytes` each save and restore five callee-saved registers around
one call. The kernel clamps both offsets and the count before it compares.
The fixed cost dwarfs the bytes compared. GNU `sort` retires 777,146,324
instructions on the same input.

## Tests

`TestSelfHostMismatchBytes` runs lengths from 0 to 97 on x86-64, arm64
and wasm. It puts the differing byte at each position and tries each
offset. The IR goldens of the kernel are unchanged: the arm is in the
emitted kernel, not in the IR.
