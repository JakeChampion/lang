# 2026-10-07 — a repeated operand is computed once

`ssa.merge_duplicates`, in `register_form` before `fuse_rotates`. Refs
#10615.

## What changed

`std/crypto`'s md5, sha1, sm3 and blake2b rounds write each rotate's operand
out twice, as in `(e >> 25 | e << 7)` where `e` is a sum. `fuse_rotates` only
matches when both shifts read the same SSA value, so every such rotate stayed
a shift pair and its sum was computed twice. md5's kernel had 454 `addq` and
no `rorl`.

`merge_duplicates` maps a pure operation that repeats an earlier one in its
block onto the earlier one: same kind, operator and width, same operands.
Binary operations other than the four integer divisions and remainders
qualify, as do the width casts, `!` and rotates. Loads and calls never do.
The first occurrence dominates the rest of its block, so the rewrite needs
no dominance check. Constants compare by kind, value and text, across
blocks; they are only compared, never replaced.

After it, md5's kernel has 64 `rorl` and 262 `addq`.

## Measured

`cksum -a ALG` over 4 MB of random input, x86-64, callgrind Ir, both
compilers built by themselves from source. The baseline is main at
11a71739. Every digest matches GNU `cksum`.

| digest | main | this change |
|---|--:|--:|
| md5 | 166,318,209 | 101,567,697 (−39%) |
| blake2b | 201,421,673 | 118,649,623 (−41%) |
| sha1 | 196,924,992 | 155,964,411 (−21%) |
| sm3 | 329,575,183 | 291,956,989 (−11%) |
| sha256 | 296,478,444 | 296,478,489 |
| sha512 | 184,484,217 | 184,484,262 |

sha256 and sha512 write each rotated operand once.

The compiler compiling `compiler/checker.fern` for x86-64: 14,370,667,228 Ir
on main, 14,411,805,353 with the pass (+0.29%). Most of the rise is
`rewrite_through` dropping merged instructions; `ssadeps.analyze` gets 14 M
cheaper.

## Traps

- The first version keyed candidates by a spelled-out string. That cost the
  compiler 1.2%: `i32_to_string` and concatenation for every binary and every
  constant. Hashing the fields as an integer and confirming field by field
  costs 0.29%.
- A `const_int` can carry its value as literal text with `imm` 0
  (`semndarray`), so a constant's identity includes its text.

## What is left in md5's kernel

Two bounds checks per input byte, and four byte loads per message word.
