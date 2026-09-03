# The string position at the assign-form tuple rebind, on writer agreement (#7226)

The last residue of #7226. `t = (k, u)` with a `string` at position 1 stranded
**80 B/round, unbounded**; the array limb of the same rebind has released since
#7929, which restricted `emit_tup_elem_reclaim_store` to `'a'` kinds.

## Why the restriction was there, and why per-site recomputation cannot lift it

The element-kinds string is recorded ONCE, in `bind_var_slot`, from the VAR
site's literal. Every site that frees a `"TUP:"` box replays it: the rebind
store, the scope-exit sweep, the precise drop, the reuse recipient release. So
each of those replays the var site's description against a box that some OTHER
writer filled. For `'a'` that is harmless — an array element's release is one
type-fixed `rc_dec` whichever writer stored it. For a string it is not: a writer
that left a VIEW there would have its box `__fern_str_free`d out from under the
view's own `__fern_str_view_free`.

Recomputing the kind at the rebind from the rebind's own literal does **not**
fix this, and that is worth recording because it is the obvious move. A rebind
that disagrees with the recorded kinds falls through to the shallow store; it
does not disqualify the *other* rebinds. So writer 1 (owned) → box B1; rebind 2
(view) declines and stores B2; rebind 3 (owned) agrees with the recorded `'s'`
and releases **B2's view**. The scope-exit sweep has the same exposure with no
rebind involved at all.

The property is therefore whole-body: **every** writer of the slot must agree
that the position holds a box the release may claim.

## The admission

`assigns_str_pos_owned` — every `StmtAssign` to the name, recursively through
control flow, stores at each `string`/`str` position of the declaration's tuple
annotation a value `tup_str_writer_owned` accepts: `strarr_value_is_fresh` (a
literal, a concat, a string-method producer, a whole-program fresh-ret call), or
a bare ident whose SOLE `var` binding in the body is one of those and which is
never reassigned. An assignment whose value is not a tuple literal disagrees. A
name with no assignment agrees vacuously, so a tuple that is never rebound keeps
exactly the kinds it had — that is what makes this a widening and not a change to
the single-writer case.

It is credited `"TUPSTRW:"` on the binding site, and `bind_var_slot` records
`'s'` only under it. The gate is therefore on the KINDS, not on the rebind:
lifting it at the rebind alone would have left the scope-exit exposure above in
place. `tup_kinds_arr_only` is replaced by `tup_kinds_rebind_safe`, which admits
`'a'` and `'s'` and still refuses `'t'` — a struct position's release is a deep
field drop keyed on the var site's MOVE of the source, which no rebind repeats.

Views and borrowed aliases are refused by **positive proof of ownership**, not by
detection. The credit pass runs before lowering, so `is_str_view_local_slot` and
the rest of the slot facts are not available to it; a "prove it is not a view"
test would have to be an absence test over syntax, which is exactly the shape
that rots. `__fern_str_free` guards statics and `rc > 1`, so a literal writer is
admitted too and the static-literal rebind's kinds are undisturbed.

## The credit-side half, without which the fix is worth exactly half

Releasing at the rebind alone moved 160 B/round to 80, not 0. The other 80 is the
rebind's element LOCAL: `body_unsafe_for_tup_alias` forgives a `var t = (…, u, …)`
mention of `u` (the `"TUPE:"` interlock) but had no arm for `t = (…, u, …)`, so a
rebind's element was still an escape, earned no `"STR:"`, and its own box was
never swept. The assign arm is added under both `"TUPE:"` and the new `"TUPSW:"`
— the interlock may only forgive an element whose release it can count on.

An assignment carries no binding site, so that arm matches the target on the NAME
its site key holds (`tup_ok_site_named`) rather than the site. That is sound only
in this direction: a wrong match leaves the element's reference outstanding, a
leak, because the construction retain for a string element is unconditional.

**n-of-2n freed is the signature of a release-side fix missing its credit-side
half**, the same way n-1-of-n is the signature of a scope-exit-only release.

## Measured

x86-64, `FERN_LEAKCHECK=1`, `bin/fern-selfhost` from `make selfhost-cli`. Native
and interp agree on every answer, before and after; `__rc_underflow_count()` is
folded into every exit code and reads 0 throughout.

| shape | rounds | before | after |
| --- | --- | --- | --- |
| `t = (i+1, u)`, string pos | 100 | 400/200 **16000** | 400/400 **0** |
| same | 200 | 800/400 **32000** | 800/800 **0** |
| one source local at BOTH writers | 200 | 600/400 **16000** | 600/600 **0** |
| two fresh writers, `t.0 + t.1.len()` | 200 | 800/400 **32000** | 800/800 **0** |
| `(i32[], string)`, both positions rebound | 200 | 1200/600 **40000** | 1200/1200 **0** |
| both sources read after the rebind | 200 | 800/400 **32000** | 800/800 **0** |
| conditional rebind | 200 | 700/400 **24000** | 700/700 **0** |
| array-position control | 200 | 800/800 **0** | 800/800 **0** |

Refusals, unchanged in both directions and balanced with zero underflows:

| refused writer | rounds | before | after |
| --- | --- | --- | --- |
| string LITERAL at the rc position | 200 | 600/200 **24000** | 600/200 **24000** |
| borrowed alias `var u: string = b` | 200 | 800/400 **32000** | 800/400 **32000** |
| `slice_unchecked` view bound to a local | 200 | 1000/600 **20800** | 1000/600 **20800** |

The 2.0x per doubling on the headline row is the discriminator: a bounded strand
does not move with the loop bound. After the change it stays 0 at 400 rounds
(1600/1600) as well. Gate:
`internal/e2eselfhost/self_host_tuple_str_rebind_test.go`, three legs plus a
`FERN_SANITIZE=1` re-run of every row. Non-vacuous: on the parent every
string-position row fails on `live_bytes` (the array-position control passes, as
a control must), and the two round counts fail at 16000 and 32000.

## Trap: do not reach for `.trim()` or a bare `slice_unchecked` to build a view

The checker separates `str` from `string`, so a view **cannot** reach a
`string`-annotated tuple position at all — `var u: string = b.trim()` is E003 and
`slice_unchecked` in a `string`-returning function is E002. The view row here
therefore annotates the tuple `(i32, str)`. Note also that the compiler's own
view CREDIT (`str_view_local`, `__fern_str_view_free`) does not fire on either
shape: no `__fern_str_view_free` call is emitted for the row's `u`, so what it
witnesses is the refusal, not a view sweep racing one.

## A separate divergence found on the way, NOT fixed here

`t.1.len()` where the tuple annotation spells position 1 `str` answers **half**
the right length on the self-host, on every round count, with no rc involvement:

```fern
function (s: string) tail(n: i32): str { return slice_unchecked(s, n, s.len()); }
function main(): i32 {
    var b: string = w("cd");            // len 54
    var u: str = b.tail(2);             // len 52
    var t: (i32, str) = (0, u);
    return t.1.len() + b.len();         // native/interp 106, self-host 80
}
```

Binding the element out first (`var v: str = t.1; v.len()`) agrees with both
oracles, and so does the same shape with `string` in the annotation, so it is the
`str` SPELLING of a tuple element tag that a method-dispatch consumer does not
normalise. Identical before and after this change (verified on the parent), and
the plausible fix — normalising `str` to `string` in the tuple element tags —
moves every view position from the never-free class into the owned class, which
is a wide rc blast radius and belongs in its own PR with its own measurement.
