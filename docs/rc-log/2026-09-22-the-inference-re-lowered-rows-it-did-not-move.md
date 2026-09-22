# The ownership inference re-lowered rows it did not move

2026-09-22 — #9969. The fix for the largest attributable step in #9963's
allocation drift. Refs #9931, #4451.

## What it was

`semlower.inferred_pass` asks for a second lowering whenever the ownership
inference moves any parameter, and the second lowering was whole-module. On a
real module something always moves, so `ssasem.analyze`, `ssaunits.plan` /
`verify`, `ssarc`, `ssadeps` and `ssalive` all ran exactly twice for every
declaration — including every declaration the inference left alone.

`semsource.modes_differ` was module-granular, so it answered "somewhere" and
the caller re-lowered everywhere. Its own docstring aimed at the right thing —
skipping a second lowering "that would produce the same bodies" — and on a
module with one changed parameter, the bodies that would be identical are most
of them.

## How it was found

Not by reading the code. #9963 found the alloc lane 82% behind its baseline and
the notes in `refs/notes/perf` put the largest single step at +8.92% on
`6a217f9fa` (#9931). The step reproduces **larger** on a smaller subject, which
is what made it traceable at all — `FERN_ALLOC_SUBJECT=examples/self_host/lexer.fern`
gives +19.26%, over a workload of 3.1 M events rather than 88 M.

`FERN_RC_TRACE=1` at compile time on a `-g` build either side of the step,
aggregated by `(site, caller)` resolved against `nm -n`, then names it without
a hypothesis:

```
   141549 (+)    141549 ->   283098  site=__fern_arr_push_grow caller=__fn_ssaunits__bits
    36055 (+)     36055 ->    72110  site=__fn_ssaunits__step caller=__fn_ssaunits__plan
    24557 (+)     24557 ->    49114  site=__fn_ssadeps__pred_positions caller=__fn_ssadeps__dominators
    23952 (+)     23952 ->    47904  site=__fn_ssarc__bracketed caller=__fn_ssarc__block_body
    14136 (+)     14136 ->    28272  site=__fn_ssasem__analyze_parents caller=__fn_ssasem__analyze
     7376 (+)      7376 ->    14752  site=__fern_arr_push_grow caller=__fn_ssalive__zeroes
```

**Every increased row exactly doubled**, all of them inside the semantic RC
pipeline, and the lift, the register allocator and the peepholer flat. A row
doubling is a pass running twice; nothing allocating more per call looks like
this. The trap worth recording: read `caller`, never `site` alone — `site`
credits 300,195 of these to `__fern_arr_push_grow`, which is one shared
allocator and names nothing.

## The measurement

`scripts/selfhost-alloc-bench`, `FERN_ALLOC_SUBJECT=examples/self_host/lexer.fern`,
x86-64:

| | allocs | frees |
| --- | ---: | ---: |
| before #9931 (3930ca1e5) | 3,154,219 | 2,754,733 |
| main (eca2eb820) | 3,778,855 | 3,303,657 |
| this change | **3,387,224** | **2,953,147** |
| this change, `FERN_SEM_REUSE=` | 3,780,010 | 3,303,756 |

−391,631 allocs (−10.36%) and −350,510 frees (−10.61%) against main, which is
63% of the step. The rest is rows the inference genuinely moved, plus their
callers, which have to be lowered again.

The off column is the non-vacuity proof, not a convenience: 3,780,010 lands
within 1,155 of main's own figure, so `FERN_SEM_REUSE=` really re-lowers and
the equality gate below is comparing two different paths rather than the reuse
with itself.

## Why a kept row is the same row

`ssaunits.plan(f, modes)` reads its row's graph and modes and nothing else, so a
row whose modes and called contracts are both unchanged plans identically.
`ssarc.lower(f, modes, plan, structs, grows)` additionally reads the module's
grow table — but only through `grow_fields_of(grows, ins.str, i)` in
`bracketed`, keyed by the name each call instruction names. So a body comes
back exactly when the grow rows of the callees it names agree.

That last clause is the one that can be wrong quietly, and it is why the reuse
predicate is not just "this row's modes did not change". A moved row can widen
a callee's grow mask; a caller of that callee then needs re-lowering although
its own modes are untouched, and keeping its body would **drop a bracket** —
a use-after-free in the compiled program, not a refusal. `ssasem.called_names`
plus `ssaunits.grow_rows_agree` are what test it.

`inferred_produced` also rewrites every row's `func.calls` whether or not that
row's parameters moved, so `changed_rows` compares the called contracts as well
as the row's own modes. A predicate that read modes alone would keep bodies
whose callees had moved.

## Tests

`TestSelfHostInferredReuseIsIdentical` (`internal/e2eselfhost`) compiles five
compiler modules — lexer, checker, ssarc, ssaunits, semsource — with the reuse
and with `FERN_SEM_REUSE=`, and compares the **whole emitted module** byte for
byte. Whole-module rather than a grep for a helper name, because a dropped
bracket is an absence and a grep for an absence is what misses it. Both sides
are checked non-empty first: two empty emissions compare equal.

**Which of the five actually carries it: only `checker.fern`.** Measured by
deleting the grow-rows clause from the reuse predicate and re-running — that
module fails (13,939,274 bytes reused against 13,902,051 re-lowered, first
difference inside `__fn_call_through_fn_value`) and the other four still pass.
So the clause is load-bearing, the gate does catch its removal, and the
justification originally written beside `ssarc.fern` — that its call density
loads the grow-mask dependency — was false. A negative control is the only
thing that could have said so: every one of the five passes when the code is
right, which is exactly the shape that hides a case proving nothing.

Measured beyond the gate: the self-host compiler built from this tree and from
`origin/main` emit byte-identical assembly for all ten of lexer, parser,
checker, irlower, ssarc, ssaunits, semsource, semlower, ssa_lift and asm_ir.

Only a module the typed pipeline lowers WHOLE can reach any of this —
`inferred_pass` runs the inference only after `lowers_whole_module` passes on
the first lowering, so a mixed module never gets a second lowering at all.

## What it does not reach

The 63% is the ceiling of reuse, not of the fix: what remains is real work on
rows that moved. Getting below it means a smaller re-lower set — a moved row
whose grow mask did not widen does not actually change its callers' brackets,
and `grow_rows_agree` already knows that per callee while `changed_rows` does
not yet use it to spare a caller whose own modes are untouched.

And this is one step of #9963's 82%. The 48.5 M to 78.9 M half has no CI record
at all, and the mechanism found here — a whole-module re-run rather than a hot
site — suggests that half is more of the same kind rather than one place to
look.
