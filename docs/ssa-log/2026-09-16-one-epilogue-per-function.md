# Both SSA backends: one epilogue per function

**Date:** 2026-09-16

## Why

Emitting call-frame information (#9495, #9500) made the cost of this emitter's
shape visible. It returns from every block that ends in a return — 17,766 sites
on x86-64 and 16,669 on arm64 across the self-host driver, against roughly
4,800 functions — where the stack-machine emitter jumps every return to one
epilogue. Each of those sites carried a full teardown AND its own CFI bracket,
because blocks are emitted in layout order and a rule left in effect would
describe whatever block came next.

With CFI on both sides of the comparison, the x86-64 SSA driver came out
**larger** than the stack-machine one, which is the opposite of the reason this
backend exists.

## Change

Every return puts its value in place and then reaches the function's single
epilogue: by falling into it when its block is last in layout order, and by a
branch otherwise. The epilogue restores the callee-saved registers, releases
the frame and returns, once.

The CFI bracket goes away entirely. Nothing follows the epilogue, so there is
no rule to remember and restore — the saving is 3 rules per return site
replaced by 1 per function.

Both backends' marker machinery goes with it. The body no longer carries a
placeholder line for a spliced teardown, so `restoreMarker` / `teardownMarker`
and the splicing halves of `writeBody` are deleted; what remains is
`copyLines`, which exists only so a multi-line string the emitter wrote as a
unit still reaches the writer one line at a time. The callee-saved scan is
unaffected: it reads the body, and the restores were always rendered after it.

A function with no return at all gets no epilogue, so nothing emits a label
nothing jumps to.

## What it is worth

The self-host driver, `examples/self_host/asm_run.fern`:

| | size | `.eh_frame` | the rest |
| --- | ---: | ---: | ---: |
| x86-64, stack machine | 8,294,824 | 153,880 | 8,102,476 |
| x86-64 SSA, teardown per return | 8,684,307 | 282,128 | 8,363,711 |
| **x86-64 SSA, one epilogue** | **8,459,027** | **153,888** | **8,266,671** |
| arm64, stack machine | 10,113,457 | 160,128 | 9,914,853 |
| arm64 SSA, teardown per return | 9,147,195 | 241,680 | 8,867,047 |
| **arm64 SSA, one epilogue** | **8,753,979** | **136,864** | **8,578,647** |

The `.eh_frame` cost is paid back in full: x86-64 lands 8 bytes from the
stack-machine emitter's, and arm64 lands 23,264 BELOW it, because that emitter
describes two saved registers per function where this one describes x30 alone.
The teardowns themselves account for the rest — 97,040 bytes of x86-64 text and
288,400 of arm64 text.

Against the stack-machine emitter the SSA driver is now 13.4% smaller on arm64
and 2.0% larger on x86-64, where it was 9.6% smaller and 4.7% larger. The
x86-64 gap is no longer unwind data at all; it is 164,195 bytes of text, which
is the next thing to look at there.

## Tests

- `TestEpilogueRulesSitAtTheirInstructions` on both backends: the function ends
  in its epilogue with each rule at the instruction it describes, and a
  two-return function has exactly one `ret` with one jump to it.
- `TestEveryFunctionCarriesBalancedCFI` on both backends now requires ZERO
  remember/restore, which is the property that pays for the change.
- `TestCopyLinesFeedsTheWriterOneLineAtATime` replaces the marker-splicing
  tests on both backends, keeping the one property those guarded that still
  applies.
- `TestFusedBranchIsCmpAndJccAlone` looked for no `jmp` at all; a return that
  does not sit next to the epilogue reaches it by one, so it now looks for no
  jump BETWEEN BLOCKS, which is what branch fusion is about.
- `TestX86_64SSA*` over `internal/e2e` green (164 s) and `TestArm64SSA*` green
  (225 s), both including the corpus differential against the stack-machine
  emitter — 328 of 328 agreeing on each target.
