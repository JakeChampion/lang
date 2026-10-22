# 2026-10-22 — the call probe reads a binding by index, and the lift joins lifted names once

`checker.check_call_expr`, the `use` callee check, `lift.lift_lambdas_view_typed`
and `ir.zg_first_write`. Refs #8171.

## The shape

The string-concat kernel has 528 M Ir of self time in the whole-compiler
profile. `Scope.lookup` is one of its callers that discards what it
builds. A miss, which every plain call is, returned
`t_unknown("undefined identifier:" + name)`. Two call checks asked it
whether a callee is a function-typed binding. Both matched the result on
`TypeFunc` alone, so the string, the box and the release were for
nothing: 281 K per self-compile.

The lambda lift rebuilt its global name list and its signature table on
every round of a function that lifts a lambda. Each rebuild copied the
module's 30 K names and 30 K signatures and retained every element: 586
copies per self-compile, 67 M Ir in the element retain alone.

`zg_first_write` grew its slot table by one append per slot.

## What changed

Both call checks read the binding by `binding_index` and match its type
when there is one. A miss builds nothing. `lift_lambdas_view_typed` keeps
the name list and the signature table across the rounds of one function.
`acc` only grows within a function, so each lifted function joins both
once, on the round that lifts it. `zg_first_write` takes its table from
`__alloc_i32` at the slot count.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at ddc5b11.

| subject | #12949 at a1fd679 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.393 G | 74.299 G (−0.13%) |

The whole-compiler binary is byte-identical to the base compiler's.

## What is left

The first round of a lifting function still copies the two tables once.
`lambda_has_no_captures` scans the 30 K names with `index_of_str` per
identifier a lambda uses, where `gset`, the index the lift already
builds, answers the same question. The IR peephole pipeline
(`optimize_ops` and the passes under `cleanup`) is 4.6 G inclusive, 6.2%
of the compile, over about ten passes per function. Each pass copies from
its first change. The next gain there is fewer passes, not cheaper ones.
