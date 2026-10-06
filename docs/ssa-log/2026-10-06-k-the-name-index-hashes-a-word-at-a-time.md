# 2026-10-06 — the name index hashes a word at a time

`util.hash_bucket` on the `__str_hash` kernel (`2026-10-06-j`). Refs #8171.
No emitted byte changes: the compiler before and after builds `checker.fern`
for x86-64, arm64 and wasm, and `fern.fern` for x86-64, byte for byte,
which also says that no table in the compiler depends on which bucket a
name lands in.

## What changed

`hash_bucket` was the polynomial roll `a = a * 31 + byte` over the bytes,
six instructions a byte, which is as tight as a byte loop gets; it is now
`(__str_hash(s, 0) & 0x3FFFFFFF) % n`, a xor and a multiply per 8-byte
word. The function keeps its `n <= 1` short-circuit and its mask, so the
bucket index stays non-negative. The per-module cache key
(`modloader.module_fact_hashes`) sums it, so every cached unit misses once.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 9d3616f7 (the kernel's merge) by
the pin, so the pin does not enter the comparison.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.021 G | 17.863 G (−0.87%) |
| `util.hash_bucket`, self | 340.4 M | 186.9 M |

Nothing else moves by more than a megainstruction. The 187 M that stay are
3.2 M calls at 58 instructions each: the kernel's loop is 6 per word, and
the rest is the call, the `%` and the stack-machine bridge the register
path runs a kernel op through (push the operands, pop the result).

## What is left

The bridge is the next cost in this function: a register-path arm for the
kernel ops, taking their operands where they live and leaving the result
in a register, would take most of the 58 off every kernel call, not only
this one. Beyond that the lookups themselves: `NameIndex.find` is 1.5 M
calls, and `type_from_spelling` and `Scope.lookup_struct` hash a spelling
the checker has usually just hashed.
