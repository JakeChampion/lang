# 2026-10-13 — a variant test compares its shape as an immediate

x86-64 only. `asm_ir.ssa_shape_plan`, `ssa_alu_is`; `ssa.Imms.sym_at`;
`x86_native` absolute imm32 code fixups (`afix_*`, the `\x08` record,
`cmpq $sym, %reg` in the text). Refs #8171.

## The shape

Every `match` arm, every `if let`, every `variant_is` the lowering emits tests
the scrutinee's shape word against the variant's interned shape address, by
pointer equality. On x86-64 that was two instructions, `leaq .S<idx>(%rip),
%r11` then `cmpq %r11, %reg`, with the branch fused onto the compare. Over
one stage-2 compile of `checker.fern` the tag tests were 4.1% of all
instructions — 1.28% the leas, 1.28% the compares, 1.55% the branches — the
compare chain of `astwalk.fold_expr_nodes` and `semtypes.equal` at the top.

## What changed

The executable is ET_EXEC at 0x400000, so every shape address fits a
sign-extended imm32. A shape constant (kind 46) whose every use is an `eq` or
`ne` whose other side is a register is now an immediate of the compare,
`cmpq $.S<idx>, %reg`, and is never loaded: the x86 frame marks it in the
immediate table with its label (`ssa.Imms.sym_at`), the register allocator
gives it no home, and the fused and the `sete` forms both read it through
`ssa_rhs` / `ssa_alu_rhs`; a spilled scrutinee is compared in its slot,
`cmpq $.S<idx>, disp(%rbp)`, as it was against the loaded address (a third
of the compares read one, and the register form alone measured −0.85%). The
assembler takes the symbol as a fourth kind of
fixup, an absolute 4-byte code relocation resolved with the `.quad` data
relocations once the load address is known, in the text (`$sym` on a group-1
op) and as a record (`\x08`, `named_abs`). A shared object moves with its
load base, so `-shared` keeps the lea; the shared link refuses an absolute
code fixup should one ever reach it. A shape read for anything else — a
construction stores it into the new box — keeps the lea as well.

## Measured

The stage-2 compile of `checker.fern` to a binary under callgrind, each
compiler built from its own tree by itself, both compiling one frozen copy of
main at 6fdb1671, warm certifier memo.

| | Ir |
|---|--:|
| a masked hash (#12322) | 14.748 G |
| shape as immediate | 14.582 G (−1.12%) |

The compiler rebuilds itself byte for byte (stage2 == stage3). The compiled
`checker.fern` changes, as it must: every variant test in it is one
instruction shorter.
