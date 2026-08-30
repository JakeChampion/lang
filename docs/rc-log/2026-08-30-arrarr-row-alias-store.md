# 2026-08-30 — three credit widenings missed a store-accounting bug (#7805, #7810, #7812, #7814)

The reusable part is the search, not the fix: how it was found IS the finding.

## The leak

An append-built `i32[][]` with a row bound out of it inside the loop leaked, and
three successive widenings of the arr-of-arr RECLAIM CREDIT improved it without
closing it. The bug was never in a credit. It was in `emit_arr_store`, which
every array overwrite in the compiler goes through.

Minimal shape — no nested array, no append, no element read:

```fern
var a: i32[] = [n, n, n, n, n];
var b: i32[] = [7];
while (i < 3) { b = a; t = t + b[0]; i = i + 1; }
```

| iterations | self-host | native |
|---|---|---|
| 1 | `2/2/0` | `2/2/0` |
| 2 | `2/1/64` | `2/2/0` |
| 3 | `2/1/64` | `2/2/0` |
| 5 | `2/1/64` | `2/2/0` |

One execution clean; two or more strand exactly one buffer, constant however
many times the loop runs.

`emit_arr_store` retained the new reference (alias_inc) and released the old one
under a cow guard, `if (old != new)`. That guard is right for a self-MUTATION —
an in-place mutator hands back the same buffer, created no second count, and
releasing it would free the live value. It is wrong for a self-ALIAS, which did
create one. `alias_inc` already distinguishes them, so the release is now
unconditional when it fired and cow-guarded otherwise. Native states the same
rule on its Map twin (`internal/ir/ir.go`, the `new == old` arm): "a release is
owed only if an alias inc created a second count for it; a self-mutation created
none". The unconditional form subsumes the `old != new` arm, so that path emits
FEWER ops than before.

## The search, and why three fixes missed

Each step measured real progress and each was a real bug, so nothing looked
wrong until the residual refused to close:

| step | what it widened | the shape's leak after |
|---|---|---|
| #7805/#7810 | index row reads no longer forfeit the ARRARR credit | 48000 -> 9600 B |
| #7812 | transient `for row in g` iteration admitted | 9600 B |
| #7814 | the store's release, not any credit | 0 |

**A residual that survives successive correct fixes to one layer is evidence the
bug is in another layer.** Three widenings moved it 5x and then stopped; that
plateau was the signal, and it was visible two fixes earlier than it was read.

What actually broke the framing was the CONTROLS, not the repro. Measured on
both compilers:

| shape | self-host | native |
|---|---|---|
| flat `var xs: i32[] = []` grown by append | `4/4/0` | `4/4/0` |
| append-built `i32[][]`, no row bound | `10/10/0` | `10/10/0` |
| literal-built `i32[][]` with a row bound | `3/3/0` | `3/3/0` |
| append-built `i32[][]` + a bound row | `10/8/80` | `10/10/0` |

Every ingredient of the "arr-of-arr row" story is individually clean. A story
that needs two unrelated features present at once to fire is usually a story
about neither. `held = g[0]` in a loop returns the same row pointer every
iteration — it was a repeated self-alias wearing a hat.

## Traps, all of which cost time here

- **`FERN_LEAKCHECK` cannot gate this class.** The 2026-08-22 entry records a
  widening whose byte counts stayed clean — `allocs == frees` at
  `live_bytes == 0` — straight through a double free, because a doubly-released
  block goes back to the freelist. Only `__rc_underflow()` dissented. Every fix
  in this arc was gated on `FERN_RC_UNDERFLOW_TRAP=1` plus exit-code agreement
  with native, and the leak counts read second.
- **Two same-sized blocks in an `FERN_RC_TRACE` dump cannot be told apart.** The
  first reading of #7805's trace reported the block fates BACKWARDS — outer
  buffer stranding, row reclaimed — because both rows were 40 bytes and the
  order of allocation was used to guess. Sizing the rows differently (1 element
  vs 9) made it unambiguous and reversed the conclusion. Vary one dimension and
  watch which size moves; do not infer identity from order.
- **An out-of-bounds probe aborts at exit 134 on BOTH compilers**, and its
  partial trace looks exactly like extra stranded blocks. A probe that indexes
  `g[1]` on an iteration where `g` has one element produced a phantom "binding a
  later row leaks more" finding. Check the exit code before reading the trace.
- **A comment in a NEIGHBOURING class is not evidence about yours.**
  `reclaimable_optaarr` says native's Index-shape dup "is unported for ARRAYS …
  it retires with that dup", which was read as applying to the plain arr-of-arr
  row read and produced a whole wrong design. It is about OPTAARR, whose
  elements are option boxes. For the arr-of-arr row the dup is already emitted —
  one `rc_inc` for `var row = g[i]`, one for `row = g[i]`, ZERO for
  `for row in g`. Counting the incs settled in one command what reading the
  comment got wrong.
- **`arrarr_row_escapes` has three callers.** The first #7810 patch edited the
  shared predicate; OPTAARR and ARRTUP elements are option and tuple BOXES that
  the `is_arr` retain does not cover, so forgiving index reads there converts a
  sound leak into an over-release — invisible to the byte counts, per the first
  trap. It is now one walker behind a `dup_at_index` flag, with only the ARRARR
  credit passing the forgiving form.

## State after this arc

The arr-of-arr row class matches native on every probe measured: the minimal
alias, both index bind spellings, the escaping-struct field, transient
iteration, and the 200-round self-append rebind churn (`1800/1800/0`, from
`1800/800/48000` at the start).

One refusal is deliberate and stays: `for row in g` where the BODY lets the loop
var escape. The iteration binder emits no dup, so the row outlives the loop with
no counted reference; it leaks soundly and its exit code agrees with native.
Closing it needs a retain at the for-binder — the obvious next increment in this
area, and the only one left named.

Gates: `internal/e2eselfhost/self_host_arrarr_row_read_reclaim_test.go` and
`self_host_repeat_alias_store_test.go`. The second one's `self_mutation_keeps_guard`
case is the load-bearing one — it is what fails if a future change drops the cow
guard wholesale instead of conditioning it on `alias_inc`, and it is asserted on
exit-code agreement rather than bytes for the reason the first trap gives.
