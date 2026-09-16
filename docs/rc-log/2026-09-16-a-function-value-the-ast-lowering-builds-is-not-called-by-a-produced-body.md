# A function value the AST lowering builds is not called by a produced body

#9414. With both halves of #9407 closed, the compiler built through the
semantic lowering compiles `lexer.fern` byte-identically to the AST build and
segfaults 18.6 s into the whole self-host tree, at a 1 GB peak: a wrong
release, not memory. `checker.fern` alone reproduces it, and the sanitized
build turns the fault into a use-after-free abort with a stack.

## What the trace said

The abort is `astwalk.scoped_reads` releasing the elements of the temporary
array `reads(e, [])` handed it. A hardware watchpoint on one element, the
identifier `a`, showed its count going 3, 2, 1, freed, every decrement that
release, and no retain from the push that put it in the array:

| frame | lowering | what it does to the identifier |
|---|---|---|
| `astwalk.ident_of` | AST (`fold_expr_pruned` is a generic this path does not instantiate) | `acc.append(id.name)` through `__fern_arr_push`, no retain |
| `collect_lambda_idents$wrap0` | AST | the trampoline that makes `collect_idents_expr` a value |
| `astwalk.scoped_reads` | produced | owns the call's result; when it is unique, releases each element, then the buffer |

The AST lowering's push never retains the element it appends: its buffer
leaks instead, and every AST consumer of such an array releases the buffer
alone. The produced lowering counts every element of every array it owns.
Across the boundary those two contracts meet on one array, and the produced
side's release is the extra one. Three passes of the checker over the same
lambda body freed the identifier under the AST, and the fourth read it.

`semlower.prune` already refuses a produced body that calls an AST-lowered
body directly, for exactly this reason. Here the AST body arrives as a
FUNCTION VALUE, built by an AST-lowered caller (`collect_lambda_idents`) and
handed down as a parameter, which the direct-call rule cannot see.

## What landed

`prune` reads the function values each produced body calls (`Rows.called`,
the callee value's type at every indirect call) against the function values
the AST lowering builds (`ast_values`: every hoisted body — `$wrap`, `$clo`,
`$iife` — this substitution is not emitting, typed through its contract's
`closure_type` where the declaration has one, by arity where it does not).
A produced body that calls a value of such a type is turned off, in the same
fixpoint as the direct-call rule: a body turned off in one round is a value
the AST lowering builds in the next. The builder exports its contract table
(`Built.contracts`) for the type.

The match is by type, not by flow: a data-flow answer would say which values
reach which parameter, and this says which could. Over-refusal is the safe
direction, and a body it refuses falls back to the AST lowering, where the
whole chain is AST and its leak keeps the elements alive.

`TestSelfHostSemanticProduction` gains `ast-value-into-produced`, whose skip
leg keeps the callback on the AST lowering and asserts the report turns the
produced consumer off by the value's type.

## Measured

On the production test's program with the callback on the AST lowering, the
report reads, in this order: the trampoline turned off for calling the
callback directly, the consumer for calling a value of the callback's type,
then the caller of both. On the compiler itself: the count of produced
bodies before and after, and the produced compiler on `checker.fern` and the
whole tree, are recorded once the self-build with the rule reports.

## Traps

**The small program does not abort.** Six reproducers of the shape — an
AST-lowered callback appending a projection, handed as a value to a produced
consumer that drops the result — ran clean under the sanitizer, in every
mixed configuration. The compiler's own chain has the generic fold and its
trampolines between the two, and whatever keeps the small programs' counts
balanced is not in the contract. The evidence is the watchpoint on the
compiler, not a fixture: a boundary rule is tested by the report it writes.

**A watchpoint on the count word needs the 32-bit read.** The count is the
low half of the header word; the high half is a tag, so `*(long*) == 0`
never matches and the run ends at the abort with nothing printed. Watch the
`int`, and print on every write rather than on a condition the free path
may skip.

**The trace's caller column is the invoker's return address.** For a free it
names the frame ABOVE the one that called the runtime helper, so it read
`scoped_stmts_reads` for a release `scoped_reads` made. The backtrace at the
abort, or the watchpoint's, is what names the frame.
