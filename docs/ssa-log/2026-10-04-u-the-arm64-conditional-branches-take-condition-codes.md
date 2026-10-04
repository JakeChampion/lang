# 2026-10-04 — the arm64 conditional branches take condition codes

`asm_arm64_ir` and `arm64_native`, arm64. Item 5 of #11452, refs #8171.

## The shape

The SSA emitter handed each conditional branch to the record helper as a
mnemonic string: `"b.ne"`, `"blo"`, or `"b." + ir_cmp_cond(...)` built by
concatenation. `arm64_rec_bcond` then parsed it back. It checked the spelling,
cut out the suffix (`arm64_gas_bcond_suffix`, which allocates) and compared it
against the condition names (`arm64_gas_cond`). That cost 477 instructions per
branch.

Now the emitter passes the condition code:

- `ssa_bcond` and `ssa_trap_guard` take a code. The call sites name it with
  `CC_*` constants.
- `ir_cmp_cc` gives the code for an IR comparison. Negating a condition flips
  its low bit, so one table serves both senses where `ir_cmp_cond` kept two.
  `ir_cmp_cond` is now the code's name.
- `arm64_rec_bcond(cond)` encodes the code directly. `arm64_cond_name` gives
  its spelling for the text path, which always writes the `b.<cond>` form.

## Measured

`checker.fern` built for arm64-linux by the stage-2 compiler under callgrind,
main at 1c73dd85 against this branch. Both compilers build `checker.fern` to
byte-identical binaries for arm64-linux, arm64-darwin and arm64-android, with
and without `-g`, and `fern.fern` for arm64-linux:

| | main | condition codes |
|---|--:|--:|
| stage 2, arm64-linux target, total Ir | 19.532 G | 19.484 G (−0.25%) |
| `ssa_bcond`, inclusive | 52.4 M | 16.8 M |
| `arm64_rec_bcond`, inclusive | 37.7 M | 1.5 M |
| `ir_cmp_cond` / `ir_cmp_cc`, inclusive | 10.9 M | 2.5 M |

## Register names are not converted

The rest of item 5 would hand the record helpers register numbers rather than
names. `arm64_rec_gpr` costs 33 M in all, about 20 instructions per operand,
or 0.17% of the compile. The emitter writes 283 register-name literals, so
converting them is a large change for a small gain, and it is not done.

This finishes the assembler line of #11452. The x86 assembler is now 4% of
the compile, so even removing it entirely would save less than one structural
change elsewhere would.
