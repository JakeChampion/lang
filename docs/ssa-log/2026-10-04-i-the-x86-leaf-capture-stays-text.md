# 2026-10-04 — the x86 leaf capture stays text

`asm_ir` and `asmcore`, x86-64-linux. A slice of #11305 measured and
dropped, refs #8171.

## The shape

A spill-free x86 leaf is emitted into a capture before its frame is chosen:
`ssa_needs_frame` scans the captured text for a call, a push, a pop or an
operand naming %rbp or %rsp, and the body is written behind a frame only
if one is found. A capture takes text alone (`records()` is false inside
one), so these bodies were most of the x86 text left after slice 9:
checker's listing carried 25,941 text lines, about 4,000 of them in
leaf bodies.

## What was tried

`EmitState` took a second record buffer for a capture, and the record
writers (`word`, `bytes`, `named`, `mark`, `branch`, `lab_def`, `lab_ref`,
`cfi`) chose between it and the module's. The frame decision for the
records moved into the helpers: each of the 21 x86 record helpers set a
`frame_need` flag on the state when its instruction called, pushed, popped
or named a frame register while a capture was open, and the scan kept
reading the lines that stayed text. After the frame was chosen, the
captured text went out as before and its records were spliced in behind
it. The flat-op capture, which reads an arm's text back, kept text alone.
The binary was byte-identical on both targets.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at a1f0f519:

| | main | first cut | flag checks on the record paths |
|---|--:|--:|--:|
| stage 2, x86-64 target, total Ir | 20.376 G | 20.431 G (+0.27%) | 20.382 G (+0.03%) |

The first cut computed `ssa_frame_reg` string compares eagerly at the head
of every helper, so all 780,000 instructions paid for a check only
captured ones needed. Moving the check onto the record path, as integer
compares on the register numbers the helper had already decoded, and
inlining the buffer choice into each writer brought it to +0.03%.

What stayed is the shape of the trade. The text the slice removed is worth
about 20 M: `util.index_of_sub` (11 M) and `ssa_needs_frame` scanning the
leaf text, and the assembler's text path for those lines. Against that,
`EmitState.mark` stopped being inlined once it carried the capture branch
(10.5 M), the per-record flag check cost 8.3 M, and the rest (the flag
writes, `bytes`' second branch) about 7 M. Hand-inlining the check at
every site would leave a gain near 0.05%, for a second record buffer and a
flag threaded through every helper.

## What is left

The leaf capture and the flat-op splice stay text, which is the last
sizeable generated text on x86; the rest is data directives, labels by
name, the stack-machine arms and the runtime. Further gains are in the
assemblers' cores, not in moving more text to records.
