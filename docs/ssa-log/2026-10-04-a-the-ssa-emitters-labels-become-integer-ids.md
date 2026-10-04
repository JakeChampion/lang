# 2026-10-04 — the SSA emitters' labels become integer ids

`asmcore`, `asm_ir`, `asm_arm64_ir`, `x86_native`, `arm64_native`; both
native targets. Slice 3 of #11305, refs #8171.

## The shape

With the instruction records of the first two slices in place, the
assemblers' remaining cost on the stage-2 compile of `checker.fern` was
mostly labels. The listing defines about 134K labels and branches to them
about 190K times, and every one went through a name: the emitter rendered
`lp + "rcinc" + i32_to_string(n)`, the assembler hashed it into a bucket
chain to define it, hashed it again to resolve each branch, and on x86 once
more to link each line to its index. On arm64 that was `arm64_name_bucket`,
`arm64_asm_label_off` and `arm64_asm_label` at about 350 M Ir; on x86
`x86_name_bucket`, `x86_gas_label_lookup`, `x86_gas_link_labels` and
`x86_place_label` at about 305 M.

## What changed

A label the SSA emitter defines and branches to is an `asmcore.Lab`: an id
and a name. `EmitState.nlab` hands out ids; a function takes one per block
at entry (`SsaFrame.lbase`, block b at `lbase + b`) and `lab_new` mints the
rest where they are made: the rc guards' `done`, `dec` and `kept`, the edge
stubs, the release entry's three, the uniqueness join. The name is built in
both modes, so every text fallback and the `-emit asm` listing are what they
were.

Two more record lines carry the ids:

- `\x04` defines a label: eight bytes of the record buffer, the id in the
  low four.
- `\x05` branches to one: the id in the high four bytes, and in the low four
  the arm64 instruction word with no displacement, or the x86 jump kind (the
  condition code of a jcc, 16 for jmp, 17 for call).

The arm64 assembler places an id in `lid_offs[id]` and resolves a fixup to
one through `fix_ids`, beside the named table. The x86 assembler gives id i
the label index `names.len() + i`, after the named labels, so every round
places and reads it through the same `lab_secs` / `lab_offs` arrays and the
relaxation is untouched. Function symbols (`__fn_x`, the runtime helpers)
keep their names: a call still travels by name.

One trap on the way: the x86 round loop skips a line whose body is empty,
which is how a blank text line costs nothing, so a `\x05` line built with an
empty body produced no bytes at all and the checker binary came out 12%
smaller. The body is the marker byte.

## Measured

`checker.fern` built by the stage-2 compiler under callgrind, 4-core x86-64
container. Stage 2 is built from main at e3a7b384 and from this change
applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.721 G | 20.411 G (−1.50%) |
| stage 2, arm64 target, total Ir | 20.806 G | 20.352 G (−2.18%) |

What is left of the label work after the change: on arm64 the named table
(`arm64_name_bucket`, `arm64_asm_label_off`) and the id table
(`arm64_asm_fixup_id`, `arm64_asm_label_id`) together are about 115 M Ir; on
x86 `x86_gas_link_labels`, `x86_place_label` and `x86_name_bucket` are about
140 M, most of it the per-line index walk the next slice removes with the
line parse.

Both targets' binaries are byte-identical, and `scripts/selfhost-emit-hashes`
matches main for every (fixture, target) pair. `TestSelfHostWordsMatchText`
compares the text and record paths in one process: 34,692 of its arm64 lines
and 31,719 of its x86 lines now travel as records, from 26,743 and 13,931.

## What is left

The labels of the hand-emitted helpers (`.Lfr_*`, `.Lstd_*`, the string
compare and array push arms) and of the stack-op arms are still text, as is
every call. The record lines still go through each assembler's line loop,
one `GasLine` each on x86; #11305's next slice retires that parse for
generated code.
