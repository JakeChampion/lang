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
whole chain is AST and its leak keeps the elements alive. Two edges of it
the production suite drew on the first run:

- A function type with no reference in it — `(i32) => i32` — is left alone.
  Nothing in a scalar callback is released by either side, so the two
  lowerings agree on it whichever built the value (`capture-write`, whose
  closure is AST-lowered for its write back, keeps its produced `apply`).
- The mirror: a produced hoisted body whose CREATOR is AST-lowered keeps the
  AST lowering (`ast_built_value`). The box that names it is built in the
  creator, so an AST caller calls it under the AST convention, and on the
  `own-forwarded-into-produced` skip leg that was a produced `visit`
  consuming the `own` array an AST `fold` released after — 124 for 62. With
  the trampoline on the AST side, `visit` is reached by a direct call the
  existing contracts cover (`irlower.consume_sigs`).

- The mirror's cone: what an AST-lowered hoisted body reaches by direct
  call, through any AST-lowered body on the way, keeps the AST lowering too
  (`ast_indirect_cone`). The mirror alone left `own-forwarded-into-produced`'s
  skip leg faulting on the sanitize and wasm legs: the trampoline was AST,
  and called produced `visit` directly with the `own` array, a transfer
  `irlower.consume_sigs` covers — but the AST `apply` above it reaches the
  trampoline through the box, and the AST lowering's ownership analysis
  cannot see through an indirect call, so AST `forward` released the array
  `visit` had already consumed. A plain AST caller of `visit` is fine (the
  same program with the value replaced by a direct call answers 62 in every
  mix); it is the indirect call above the transfer that the analysis is
  blind to, so everything below such a call keeps the convention its caller
  assumes.

`TestSelfHostSemanticProduction` gains `ast-value-into-produced`, whose skip
leg keeps the callback on the AST lowering and asserts the report turns the
produced consumer off by the value's type.

## Measured

On the production test's program with the callback on the AST lowering, the
report reads, in this order: the trampoline turned off for calling the
callback directly, the consumer for calling a value of the callback's type,
then the caller of both.

On the compiler itself the rule alone went the wrong way, and that is the
finding. Self-build, 4-core x86-64 container:

| build | produced | produced compiler on `lexer.fern` |
|---|---|---|
| before the rule | 7,568 of 8,294 declarations, 64 of 64 instances | byte-identical; faults on `checker.fern` (#9414) |
| the rule | 7,036 of 8,307, 2 of 64 (6m04s, 7,647 MB) | faults 0.5 s in |
| the rule + the clone fix below | **8,307 of 8,307, 0 of 0** (9m26s, 8,396 MB) | byte-identical, and so are `parser.fern`, `checker.fern` and the whole tree; rebuilding itself through the path exhausts the arena 19 minutes in (exit 125) |

The second row's fault, under the sanitized build, is a use-after-free in
`flatten.collect_local_names`, with `rewrite_module_bodies`,
`flatten_qualified`, `bundle` and `load_bundle` above it — every frame
AST-lowered, and the block they touch a `FuncDecl` the produced parser built.
That is the mixed module's contract mismatch reached through a DATA
STRUCTURE rather than a call: a record built under one lowering's array
convention released under the other's, which no call-site rule can see. The
rule is still right for the direction it covers; what it cannot do is make
a mixed module sound, and each round of over-refusal here changed which
mixed shape faulted rather than removing one.

What made the module whole was upstream of this boundary.
`parser.monomorphize_module` clones a generic into `fold_expr_pruned__boolean`
with its `type_params` emptied and its `type_param_count` copied from the
template, and `semsource.generic_decl` reads either field, so every clone
was a template with nothing to bind — refused silently as a template, and
every caller of one refused as `call target was refused: uninstantiated
generic`. Tracing every refusal in the second row's report to its root:
637 are that cascade, 66 are this rule's mirror on trampolines whose
creator was such a clone, and the rest are the arity-matched function-value
rule over values those clones built. `clone_bg` now zeroes the count, and
`subst_ty` substitutes behind a function type's `own T` slot (the second
bug the first was hiding: a fold's callback still spelled `own T` in the
clone). With both, nothing in the compiler is refused, so nothing is mixed,
and the crash #9414 describes cannot arise: `ident_of` is produced.

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
