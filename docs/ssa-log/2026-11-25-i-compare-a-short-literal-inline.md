# Compare a short string literal inline

Part of #15852 and #8171.

`ssa.literals`, `ssa.eq_literal`, `asm_ir.ssa_str_eq` and
`asm_arm64_ir.ssa_str_eq`. `ssa.literals` replaces
`ssa.literal_first_bytes`.

## The shape

`op == "+"` compared the two length words, then the first byte of the
operand with the first byte of the literal. When both agreed, it called
`__fern_str_eq`. For a literal of one byte, the call always returns 1 at that point.
The interpreter compares each binary operator with a chain of such
literals, so a match paid the call on each operation.

## The rule

When one operand is a literal, the compare tests the length of the other
operand against an immediate. A literal of up to 8 bytes
(`ssa.INLINE_EQ_MAX`) then has each of its bytes compared inline, and the
compare gives 1 with no call. The empty literal needs only the length test.
A longer literal tests its first byte and then calls the helper, as before.
A compare of two values that are not literals does not change.

## Measured

x86-64 Linux, callgrind, main at `9001ecd3`. The two `-g` compilers build
the same sources. The only difference is the compiler that built them.

| Workload | Before | After | Change |
| --- | ---: | ---: | ---: |
| `fern -interp`, a loop of 300,000 turns with 4 binary operations | 2,169,031,972 | 2,096,494,178 | -3.3% |
| the same loop, `__fern_str_eq` self cost | 64,811,092 | 10,179,888 | -84% |
| compile `coreutils/tr.fern` to x86-64 | 2,751,136,860 | 2,738,166,392 | -0.47% |

The `-g` compiler binary grows from 42,326,480 to 42,383,792 bytes
(+0.14%). The `tr` binary keeps its size of 124,640 bytes.

## Tests

`TestSelfHostSSAStrEqTestsShortLiteralsInline` replaces
`TestSelfHostSSAStrEqTestsLiteralFirstByteInline`. On both ISAs, it reads
the length immediate and the bytes of each inline compare. It requires two
calls only: the compare of two values that are not literals, and the 9-byte
literal. The program also checks near misses on the last byte, on the second
byte of a UTF-8 character and on the length.

`TestSelfHostSSAStrEqComparesLengthsInline` marks `same` as `@noinline`.
Before, `same` was inlined into `main` with literal arguments. The test
now reads the compare of two values that are not literals in `same`.
