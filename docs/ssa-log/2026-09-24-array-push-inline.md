# Array appends take the fast path at the call site

Every `.append` on the register backends was a call to `__fern_arr_push`
or `__fern_arr_push_owned`, and nearly every call took the helper's first
twelve instructions: a receiver with a free slot that the push may mutate
(sole owner, or the immortal bit) stores the value, bumps the length and
returns. A stage-2 compile of `lexer.fern` made 7.3 M such calls.

Both backends now emit that fast path at the call site and call the helper
only for a full or shared receiver. With the sanitizer or the use-after-free
quarantine on (`rc_free_debug_on`) the append stays a call, as the helper's
own fast path is off there too.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 compiler, Ir | 1,507,635,909 | 1,503,638,564 (−0.3%) |
| `checker.fern` compile, stage-2 compiler, wall clock (mean of 5 alternating runs) | 9.71 s | 9.25 s (−4.8%) |
| stage-2 compiler binary | 10,926,371 B | 11,347,003 B (+3.9%) |

Both stage-2 compilers are built from main's source, one by main's compiler
and one by this change's. The assembly they emit for `lexer.fern` is
byte-identical; built with leakcheck and compiling `checker.fern`, both make
and free 104,535,236 allocations and emit the same text.

The instruction count barely moves because a call and return are cheap to
count; the wall-clock gain is the removed call, spill traffic around it and
the branch into the helper.
