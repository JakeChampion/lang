# 2026-08-30 — the ownership signature table, end to end (#7809, #7811, #7813, #7816, #7818)

#7786 asked for "a whole-program signature relation a verifier can read", and
observed that Fern already computes the ingredients as "per-function decision
tables consumed by lowering". Five slices landed it. This entry is what now
exists, what it measured, and which of the measurements were wrong first.

Every number below is over `examples/self_host/fern.fern` — 6516 functions,
0 lift failures, 10278 pointer parameters — unless it says otherwise. That
program is the basis because the conformance corpus re-lowers the stdlib once
per fixture and counts it every time.

## What exists

| | |
|---|---|
| `ir.RcHelperSig(name)` | per-argument RC effect for every callee the IR does not define — retain / release / inspect / move, which argument, and whether the result is the operand renamed |
| `ir.RcHelperUnmodelled(name)` | the helpers that move counts in a shape one operand effect cannot express, with the reason |
| `ir.Func.ParamConsumed` | the LOWERING's own ownership verdict, recorded where `computeRcAnalyses` already had every input |
| `ssa.SolveOwnership(funcs)` | an independent fixpoint over the call graph, from the lifted form, having seen none of the above |
| `ssa.Signature.ReturnBorrowed` / `ReturnBorrowedFrom` | Roc phase B: which returns hand back a borrow, and of WHICH parameters |

Two completeness gates keep the table honest, one per registry:
`TestRcSigsCoverEveryRuntimeHelper` over wasm's 147 runtime helpers, and
`TestEveryProvidedCalleeHasAnRcClassification` over `providedSigs`' 269. A new
name in either fails until someone decides which bucket it belongs in.

## What it measured

**The interprocedural half is worth 45% of the answer.** Local demand alone
finds 203 consumed parameters; propagating across call sites finds **368**, in
4 rounds. That is the argument for doing it interprocedurally, as a number.

**Coverage: 4886 of 663896 call sites (0.74%) are opaque, across 5 callees** —
`<indirect call>` plus the four `rcUnmodelled` helpers (`__alloc_reuse` and the
`__fern_arr_push_grow` family). All five are opaque by construction. It was
11831 across 26 before the builtins were classified; every closable name is
closed.

**The two models agree on 95.39% of pointer parameters**: 218 agree consumed,
9586 agree borrowed, 150 solver-only, 324 incumbent-only. The disagreement is
STRUCTURED, and both directions are right about different questions —
incumbent-only is dominated by `computeConsumedParams` promotions (callee-
internal, paid for by one entry retain, ABI unchanged), solver-only by `i32[]`
assembler buffers threaded through a forwarder that never reassigns them.

**Phase B: 3829 address-returning functions, 84 proved to hand back a borrow.**
The other 4492 blocked classifications are `OpAlloc` — those really do return a
unit the caller owns, so the pessimistic assumption was correct for them and is
now derived rather than assumed. No parameter mode moved as a result. The
deliverable is that a stated assumption became a measured one, and the answer
is that it was right 98% of the time.

## The traps, which is most of what is reusable here

Six measurements were wrong before they were right, and they share one shape:
**a grep, a regex, or a derived count is an instrument, not evidence.**

1. **"259 helpers"** — a substring filter over `dec` / `drop` / `free` also
   caught `hex_decode`, `is_non_decreasing`, `map_inc` and a user function
   called `mk_free`. The answer is 17 runtime helpers and one closed family of
   generated drops.
2. **"`__fern_box_free` takes a receiver"** — it is `(data, size)`, operand 0.
   The genuine non-zero operands are `__map_dec_value(buf, v)` and
   `__map_free_val_cell(buf, v)`, which release argument 1 — and both are
   defined Fern functions, so they belong to the fixpoint, not the table.
3. **`__method_Map_set` borrows its key and value** — from reading
   `emitMapSetRetains`, which emits the compensating retain CALLER-side.
   `calleeRetainsAnyArg` says the opposite and is also true: the callee moves
   a fresh argument in WITHOUT an inc. Either side alone gives the wrong
   answer. This is why `RcSig` is per-argument: Map_set consumes BOTH.
4. **"the 122 `i32[]` solver-only params are an over-release"** — measured
   clean: 11/11/0 and 103/103/0 allocs/frees/live_bytes with
   `__rc_underflow_count()` at 0 and the sanitizer silent, the second with a
   live alias read back AFTER the call. The release the solver sees is a
   decrement whose reclamation is gated on uniqueness, so at rc>1 the append
   copies and frees nothing. Conservative, not wrong.
5. **"the two native backends' builtin rename maps disagree on 30 names"** —
   a regex artifact. arm64 separates its `case` and `target =` lines with
   comments, and the tcp family uses a `"__fern_" + target` prefix rule. The
   maps agree.
6. **"10306 of 14311 `is_unique` guards have a null operand"** — an artifact of
   the LIFT: a slot's value at a block boundary can be the lazily-created
   `const 0` undef filler used for phi slots, so a def map reads that instead
   of the operand. The flat IR shows `local.load slot=11; rc_is_unique` with
   slot 11 stored before every guard. There is no such population.

Twice the runtime contradicted careful reading, and the runtime was right both
times.

## Next leads

- **The certifier (#7782 slice 3)** is what the table was the prerequisite for,
  and #7786's cost note is about that, not about this: per-path unit accounting
  summarises SETS of locals at every join, which is where Roc hit the Bell-number
  blowup. This pass has a two-point lattice per parameter that only moves up.
- **#7787 uniqueness inference.** Headroom over the flat IR: 14313 guards, 0
  conservatively elidable, 13378 with an operand slot stored 2-3 times — the
  reuse protocol's own emission shape, not scratch recycling. No cheap version
  exists; the three Roc conditions need per-path dominance and liveness. On SSA
  the first is free, since each definition is unique by construction, which is
  an argument for `docs/SSA-CUTOVER-PLAN.md` from a direction it does not list.
- **The unmodelled four.** `__alloc_reuse` and the `__fern_arr_push_grow` family
  are the whole remaining closable gap, and they cost three real answers:
  `ssa__merge_names#a` and the two arm64 GAS emitters are reported borrowed
  only because their release goes through one of them.
- **`ssa.SolveOwnership` drives nothing.** It is read-only, and its opaque
  count is the number that has to reach 0-or-explained before anything lowers
  from it: treating an unknown callee as borrowing is the assumption, and it is
  wrong in the unsafe direction.
