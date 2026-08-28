# Instrumenting the tuple wave — and a gap the instrument found

Setting up to route the tuple release families through the plan, the first
step was an instrument: a cell the routing would move. Measuring it first
changed what the wave looks like.

## The planned instrument does not discriminate

`tuple_mixed__callarg__read` — a tuple local passed to a read-only callee —
was the obvious analogue of the cell the struct wave flipped. It measures
**clean / clean** today. The credit gate carries its own interprocedural
borrowability tier, so it already admits a call arg at a borrowable
position; there was never a refusal there to lift.

That makes the row a **guard**, not a gain: it pins the behaviour the
routing must not regress. Kept for exactly that.

## The shape that does discriminate is a new gap

`tuple_mixed__callarg__stored_struct` — the callee KEEPS the tuple, in a
struct literal it returns — measures **clean / leak** on both
architectures, with exits agreeing (6 = 6) and the underflow counter at 0.

The store is a COUNTED construction, so native's caller-side release is
balanced and it frees `keep`. The self-host's credit gate reads any call
arg at a non-borrowable position as an escape and refuses, leaking the box
and its element.

This shape was not in the enumerated matrix, so the 08-28 elemret entry's
"actionable-gap count is zero" was true of what was enumerated, not of the
family. It is one row again.

## What that does to the wave's shape

The tuple routing is therefore not a no-op convergence change: it has a
real row to gain. But a follow-up pass established that the row is further
away than "route the gate", and in the dangerous direction.

**Native's store is counted; the self-host's is neither counted nor
dropped.** `needsRcIncOnAlias` has a tuple arm, so native's struct-literal
field store retains, and `genStructDropFn` emits a child drop for a tuple
field (`arrElemIsRcTracked` includes `TupleType`) — the pair that makes the
caller's release balanced. The self-host has **neither half**:
`lower_expr_struct_lit`'s `fav_alias_inc` fires for arrays, nested structs,
enums and strings and has no tuple arm at all, and `emit_ir_struct_drop_one`
has no `k_tuple` — `struct_has_reclaim_array_field` says so in its own
comment. So today's self-host is internally consistent in the LEAKING
direction: no inc, no dec, the tuple leaks with the struct, sound.

That inverts the ordering. Granting the caller's credit now is a
use-after-free, not a leak fix. The cell as written does not observe it
(`h` dies in the same frame), but this does:

```fern
function mk(): Hold { var k: (i32, i32[]) = (5, [6, 7]); return keepit(k); }
```

`k` would take its deep free at exit while the returned `Hold.t` still
points at it — the exit-99 / sanitizer class, not a matrix leak.

## The ordering this implies

1. **The co-extensive retain + drop pair** (#7253 discipline, both halves in
   one commit): a tuple arm in `lower_expr_struct_lit`, a `k_tuple` arm in
   `emit_ir_struct_drop_one` and its arm64/wasm twins, and
   `struct_has_reclaim_array_field` admitting a tuple field. Instrument: a
   CALLER-LOCAL cell (`var k = …; var h = Hold { t: k, n: 1 };`), which
   measures the pair with no interprocedural component, plus a knockout each
   way — retain-only leaks, drop-only exits 99.
2. **A `"TCNT:"` counted tier** in `param_counted_of`, folded by
   `borrow_reg_with_counted` (5-byte key, so it satisfies the `bar > 5`
   guard). Note `arrparam_use_ok`'s struct-lit arm defaults `scnt` true and
   would credit the new tier unconditionally — it needs the
   `struct_routes_field_reclaim_at` conjunct first.
3. **Then the routing**, telling BOTH escape scans: `rctuple_esc_expr`'s
   call arm consults only `"TUPB:"` today, so the shared-gate fix alone
   leaves the row refused — the standing "a class consults more than one
   escape scan" warning. Watch `emit_rctuple_deep_free`: it is not
   `is_unique`-gated, so two owners each walking the children is the exit-99
   shape the struct family needed `emit_struct_field_drops_gated` for.

The two cells stay the instrument throughout: the guard row holds clean,
the gap row flips.

## Also recorded

Nothing in the compiler changed here. Both rows were measured with
`FERN_LEAK_MATRIX_DUMP=1` on x86-64 and arm64 and pinned at their measured
verdicts.
