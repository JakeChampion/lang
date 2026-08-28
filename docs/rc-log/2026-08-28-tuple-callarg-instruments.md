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

## The pair's own instrument

Step 1 gets a third cell, `tuple_mixed__structfield__local_store`, because
the two above cannot isolate it: both cross a call boundary, so a move in
either could come from the store pair OR from the interprocedural verdict.
This one is caller-local —

```fern
var k: (i32, i32[]) = (i, [i, i + 1]);
var h: Hold = Hold { t: k, n: i };
```

— so the only thing that can move it is the retain/drop pair. Measured
**clean / leak** on both architectures, exits agreeing (23 = 23).

That gives step 1 a knockout matrix of its own, the shape the elemret pair
established: with both halves in, the row is clean; retain-only leaks
harder (a count nobody gives back); drop-only exits 99 (a dec with no inc).
If a single knockout does not move exactly this row, the halves are not
co-extensive and the grant in steps 2-3 is not safe to consume.

## Step 1 is two steps, and only the second moves the row

Scoping the drop half against the code split it again. The backends'
`__struct_drop_<T>` is emitted as raw asm, one `k_*` arm per field kind,
and **no backend has a tuple drop helper at all**: the tuple deep-free
(`emit_rctuple_deep_free` → `emit_tuple_type_child_drops`) is IR-level and
works off a LOCAL SLOT's known `tuple_elems`, not off a field inside a box.
Native is clean here precisely because it has the mechanism the self-host
lacks — `dropFnNameFor` routes a tuple field to `__drop_tuple_<mangled>`.

So the drop half has two possible cuts, and they buy different things:

| | cost | what it buys |
|---|---|---|
| **Shallow** — a `k_tuple` arm decing the box via `__fern_arr_dec`, on the `k_struct`/`k_enum` model | ~110 lines over five files (scan, assembler entry, four backend gates, retain) | frees the tuple BOX; the element buffer inside still leaks, so `tuple_mixed__structfield__local_store` stays `leak` — fewer bytes, same verdict |
| **Deep** — per-tuple-shape `__drop_tuple_<mangled>` helpers emitted in asm_ir, asm_arm64_ir and wasm_ir | a new emit mechanism, the class of `__struct_arr_elems_drop_<E>` | frees box AND elements, so the row flips |

Only the deep cut moves the instrument. That is worth knowing before
starting: the shallow cut is the natural-looking first step, matches the
existing arms, and would leave the measurement unmoved — which reads as
"the change did nothing" rather than "the change did half".

Both cuts share the same front half — the admission scan and the retain —
and that half carries the subsystem's sharpest hazard:
`strfld_reclaim_ok_types_of` is the SINGLE verdict the four backends (via
`strfldok:*` needs) and the lowering (via FnSigs) both read, and its own
comment records what a mismatch cost (#3425): a backend freeing fields the
lowering never retained, corrupting the freelist and crashing the IR-built
compiler on its first `lower_func`. A tuple tier must be seeded from that
one scan, not a second one.

## Also recorded

Nothing in the compiler changed here. Both rows were measured with
`FERN_LEAK_MATRIX_DUMP=1` on x86-64 and arm64 and pinned at their measured
verdicts.
