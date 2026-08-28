# The "TCNT:" counted tier — tuple call args stored by the callee

`tuple_mixed__callarg__stored_struct` flips `clean leak` → `clean clean`, closing
the interprocedural half of the counted-store question:

```fern
function keepit(t: (i32, i32[])): Hold { return Hold { t: t, n: 1 }; }
var keep: (i32, i32[]) = (5, [6, 7]);
var h: Hold = keepit(keep);
```

Step 2+3 of the ordering in `2026-08-28-tuple-callarg-instruments.md`. It was
only reachable because step 1 landed: the tier's admission requires the callee's
RESULT type to route field reclaim, and `Hold` began routing it exactly when the
counted struct-field store gave a tuple field a releaser.

## What was added

A fifth tier in `param_counted_of`, keyed `"TCNT:"`. A param qualifies when its
type is a deep-droppable tuple, it is not `own`, the result type routes field
reclaim, and `arrparam_uses_ok_stmts` clears the callee body under that key.

Two details the key must respect:

- **Exactly five bytes.** `borrow_reg_with_counted` does
  `slice_unchecked(row, 5, bar)` to recover the function key — the width is
  hard-coded, so a longer prefix would silently truncate the callee name rather
  than fail. The comment there now says so.
- **`arrparam_use_ok`'s struct-lit arm defaults its slot credit to true**, which
  would have credited a tuple stored into a literal whose type does NOT route
  field reclaim — uncounted, so the caller's release would over-release.
  `"TCNT:"` joins `"SCNT:"`/`"ECNT:"` in the routing conjunct, because all three
  construction retains are gated on `slit_reclaim` while the array one is not.

## Both escape scans, told two different ways

The prior entry predicted the routing would need telling to both scans and that
"the shared-gate fix alone leaves the row refused". Half right, and worth
stating precisely because the flip looked like one edit did it:

- `expr_unsafe_for`'s call arm **already** consults the merged `"CNT:"` key, so
  adding the tier told it with no routing change at all. That arm is explicitly
  the one place a tier admits where the box flag refuses.
- `rctuple_esc_expr`'s call arm consults only `"TUPB:"`, and did need the edit.
  Knocking that edit out returns the row to `leak`, so it is load-bearing.

So both scans are told; one by the fold, one by hand.

Reading the merged key is safe here even though `merge_counted_flags` ORs every
tier: only `"TCNT:"` can flag a position whose param type is a tuple, since the
array, string, enum and array-of-boxes tiers each test a type the argument does
not have.

## The arithmetic, checked in the asm

`keepit`'s body emits `__fern_rc_inc` on the param — verified in the emitted
assembly, not inferred from a balanced census. Box rc 1 → the callee's
construction retain 2 → the caller's struct loop finds it non-unique and takes
the box-only dec 1 → the rc-tuple loop walks the children and decs 0.

That order is load-bearing and now carries a note at the sweep:
`emit_rctuple_deep_free` is NOT `is_unique`-gated, so it walks the children
whatever the count. It is correct only because the struct class loop in
`emit_dec_sweep_except_list` runs ahead of the rc-tuple one. Swap the two loops
and the tuple's walk frees children the struct's drop then walks again.

## Measurements

Exits confirmed on both oracles before running the self-host:

| probe | exit | census |
| --- | --- | --- |
| `base` — store, read source after | 70 | 300/300 |
| `two_calls` — two holders via two calls | 4 | 400/400 |
| `store_and_read` — read through both owners | 2 | 300/300 |
| `handout_too` — a second callee returns `t.1` | 68 | 300/100, refused |

`handout_too` is the guard: the handout disqualifies the param under
`arrparam_use_ok`, so the caller keeps its refusal and leaks rather than freeing
an element the callee handed out.

Knockout of the `rctuple_esc_expr` edit: row returns to `clean leak`, exits
still agreeing.
