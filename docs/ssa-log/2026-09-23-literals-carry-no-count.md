# String literals carry no count

The rc plan counted a string literal as a unit the frame owns, so its last
use released it: every `k == "add"` was a `__fern_str_eq` followed by a
`__fern_str_free` of the literal. A literal's box is static with the immortal
count on every backend, so that release, and any retain of a literal, only
runs the runtime's immortal guard. In a stage-2 profile of compiling
`lexer.fern`, `ssa_lift.bin_sym` alone made 309 K such releases, and
`ircore.is_fern_helper` and `lexer.is_keyword` another 383 K between them.

`ssaunits.fresh_unit` no longer counts a literal as owned, a phi whose
operands are literals (or borrowed parameters) holds nothing, and a
consumer that takes a literal, or a phi that can only hold one, gets it with
no retain (`Plan.literal`, read by `ssarc.supplies`).

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 (self-host-built) compiler, Ir | 1,899,992,636 | 1,853,992,520 (−2.4%) |

The stage-2 compilers compared are built from the same source by main's
compiler and by this one, so they run the same compiler; the assembly each
emits for `lexer.fern` is byte-identical. Their own machine code differs,
which is the change being measured. Built with
leakcheck and compiling `checker.fern`, both stage-2 compilers make and free
99,650,193 allocations with no live bytes left, and emit the same text.
`TestSelfHostOptimisationShapes` pins the three shapes: a comparison against
a literal releases nothing, a literal handed on twice is not retained, and a
phi of literals is not released.
