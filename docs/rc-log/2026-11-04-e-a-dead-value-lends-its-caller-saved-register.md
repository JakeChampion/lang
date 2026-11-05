# 2026-11-04 — a dead value lends its caller-saved register

A coreutils lead in #8278, from the `ls` rows of 4000 names.

## The shape

`ssa.regalloc_linear_fixed` gives each value one interval, from its first
to its last position in the block layout. An interval has no holes. In
`__fern_str_cmp`, the word loop reads a word, and the exit block that
uses the word comes after the byte loop in the layout. So the word's
interval covers the byte loop, where the word is dead. The byte loop
then had no free caller-saved register for its second byte. Thus the
function saved and restored `%rbx` on each of about 47,000 calls.

## The rule

When no caller-saved register is free, the scan looks at each active
value in a caller-saved register. The value v takes that register when
v and the values in that register do not interfere: none of them is live
at v's definition, and v is live at none of theirs. This is the
liveness test that the share path already uses. v then joins that
register's chain, as a mate does.

A lend has these limits:

- v and the values in the register are not phis, and no phi reads them.
  An edge copy writes a phi's register at the end of a predecessor,
  which is outside the intervals.
- No operand of v's definition is in the register, because an emitter
  can write v before it reads its operands.
- v's interval holds no call, its own defining call included. The save
  around a soft call restores each value whose interval covers the call.
  The restore wrote the holder's old value over v. An earlier form had
  no such limit, and the compiler that it built crashed on each compile,
  in `ircore__inline_tiny_leaves`.

| `ls` of 3911 names, x86-64 instructions | before | after | GNU 9.4 |
|---|--:|--:|--:|
| whole run | 10,216,997 | 10,106,172 | 11,327,549 |

## Tests

`TestSelfHostSSADeadValueLendsItsRegister` compiles a string compare,
requires that `__fern_str_cmp` saves no callee-saved register, and runs
the program on each host target.
