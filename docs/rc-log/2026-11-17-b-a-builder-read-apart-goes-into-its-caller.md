# A builder read apart goes into its caller

2026-11-17. All targets. The change is in `seminline.splice_calls`. It
closes #16278.

## The shape

`interp.new_module_env` builds one `Env` record from its parameters. Four
functions write `Env { ...new_module_env(...), cells: c }`. The builder has
more than two callers, so the semantic inliner kept each call. The physical
lowering then made the update test the result for uniqueness and keep a copy
path. A later pass at the IR level put the body of the builder into the
caller. The result was a test for uniqueness on a box that the same function
built some instructions before.

The certifier did not prove the value that the update gave to the loop of
`interp.eval_module_argv`. `x86_64/fern_main.unproved` rose from 17423 to
17424.

## The rule

A constructor leaf with arguments that are not literals was not spliced. It
is now spliced when the caller only reads its result apart
(`only_taken_apart`). The base of a record update is not a read. After the
splice, `split_constructions` sends each field read to the value that the
builder stored. `drop_unread` then removes the record of the builder, and
the update becomes one record with no base.

A caller that passes the result on as a whole keeps the call.

## Measurement

Census of `compiler/fern_main.fern` on x86-64, and of `compiler/checker.fern`
for the other rows:

| Row | Before | After |
| --- | --- | --- |
| `x86_64/fern_main.unproved` | 17424 | 17411 |
| `wasm32/checker.unproved` | 3829 | 3828 |
| `x86_64/checker.refuted` | 4895 | 4898 |
| `x86_64/checker.missed` | 528 | 525 |

`interp.eval_module_argv` goes from 36 tracked values and 1 unproved to 23
and 0. The lifted SSA of `interp.call_assoc` goes from 845 lines to 721. No
function of `interp` tests a fresh `Env` box for uniqueness.

Witnessed: `TestSelfHostBuilderUpdateBuildsOneRecord` reads the semantic
graph, and `TestSelfHostBuilderUpdateRuns` runs the program on each target
with the leak census.

## The trap

`interp.call_assoc`, `interp.call_declared_method` and
`interp.call_func_targs` each gain one unproved value. The body is smaller,
and the plan now takes `fd.body` out of a unique `FuncDecl`. It retains
`fd.body` when the box is shared. The certifier does not decide that
shape. The branch tests the result of `__fern_rc_is_unique`, not a reuse
token. This is the next lead.

The census line now has an `at=` field for each unproved value. Use it with
`FERN_RC_CERTIFY_DUMP` to find the value in the lifted SSA.
