# A nested Option whose inner box owns a payload: the two-level drop

The other half of the nested-Option gap. `2026-08-20-nested-option-scalar-inner.md`
closed the case where the inner box owns nothing; this closes the one where it
owns an array, a string or a `string[]`, and needs a release two levels deep.

## What was measured

x86-64, `FERN_LEAKCHECK=1`, churn at 200 rounds. Every row is `frees=0` before —
nothing was reclaimed at all, not even the outer box.

| shape | native | before | after |
|---|---|---|---|
| `Option[Option[i32[]]]`, `Some(Some([i, i+1]))` | 0 | `allocs=600 frees=0 live=24000` (120 B/round) | `frees=600 live=0` |
| `Option[Option[i32[]]]`, `Some(Some(xs))` | 0 | `frees=0 live=24000` | `frees=600 live=0` |
| `Option[Option[string]]`, `Some(Some("ab"))` | 0 | `frees=0 live=16000` | `frees=400 live=0` |
| `Option[Option[string[]]]`, `Some(Some(["a","b"]))` | 0 | `frees=0 live=24000` | `frees=600 live=0` |

`live_bytes == 0` with `allocs == frees` — the array buffer, the inner box and
the outer box are all released every round. All twelve cases (four granted, eight
refused) agree with `bin/fern -interp` AND native x86-64 on the answer, on all
three backends, with `__rc_underflow_count() * 100` folded into every exit code.

## The release

`emit_nested_opt_payload_drop`: spill the inner box out of the outer, hand it to
`emit_opt_payload_drop_via` (which frees ITS payload, then it, and zeroes the
temp), then free the outer box and zero the slot. Depth-first, so no read chases
through an already-freed box.

`nested_opt_inner_freefn` names the inner release from the ptype —
`__fern_rc_dec` for a leak-safe array, `__fern_str_free` for a string,
`__fern_str_arr_free` for a `string[]`.

It routes on a NEW `OptRcFrees.inners` field rather than a marker inside
`pfrees` or `stys`. Both of those are read as a single helper name / struct type
and reach `op_call_direct` / `__struct_drop_` unchanged, so a prefixed value
there would be emitted as if it were one.

**Option only, never Result.** The drop runs after the match and never reads the
inner tag, so one blind release must serve every arm the inner box can hold.
`None` carries no payload, which makes Option's success payload the only one,
where `Result[i32[], string]` would need two different helpers at one site. That
is the same reasoning `rcpayload_option_call_ptype` already applies to the outer
box.

## The gate that mattered

The admission and the emitter alone moved **nothing** — the third build in a row
this week to measure byte-identical to its parent while being strictly closer to
the fix. The blocker was the arm-binding escape reading again, and the fix for it
is the load-bearing soundness argument here rather than a formality:

`binding_escapes_arm_scrut` admits `match (inner)` in the outer arm. It says
nothing about what that nested match itself BINDS. For a scalar inner the binding
is a COPY that cannot outlive its arm, which is why the scalar half needed
nothing more. For an rc inner it is a POINTER:

```fern
var held: i32[] = [];
var o: Option[Option[i32[]]] = Some(Some([i, i + 1]));
match (o) {
    Some(inner) => { match (inner) { Some(v) => { held = v; }, ... } },
    ...
}
acc = acc + held.len();          // reads a buffer the drop would have freed
```

`nested_opt_payload_arm_escapes` closes it by running
`opt_body_binding_escapes` over the outer arm's body against the inner binding,
with the PLAIN reading — so a doubly-nested option is refused rather than
admitted by recursion. Pinned as `inner_payload_escapes`.

## The interlock, measured not assumed

`Some(Some(xs))` off a live local looked like the risky row and is not. The
emitted `churn` carries a construction retain:

```
n_arrlit   (literal): 3 __fern_arr_box, 0 releases,        no rc_inc
n_arrident (ident)  : 3 __fern_arr_box, 2 __fern_arr_dec,  1 __fern_rc_inc
```

so the buffer sits at rc 2 and the two-level drop's dec is the SECOND, not a
double free — the same interlock the single-level array payload already relies
on, which is why that one is admitted by annotation rather than by literal-ness.
Both spellings are granted, and the `__rc_underflow_count()` term in every exit
code is what proves it rather than the byte count: a double free would surface as
`want + 100`, which no `want` here can collide with.

## Refused, and why each one has to be

* `inner_payload_escapes` — above. This is the trap.
* `inner_none_under_rc_type` — `Some(None)` under `Option[Option[i32[]]]`. The
  drop reads offset 8 of the inner box unconditionally and a `None` box never
  stored one. `opt_arg_is_some_ctor` (not the laxer `opt_arg_is_direct_ctor` the
  scalar half uses) is what keeps it out.
* `triple_nested` — `Option[Option[Option[i32]]]`. `nested_opt_inner_freefn`
  names a release for an array / string / `string[]` inner and nothing else, so
  the walk is two levels deep by construction.
* `inner_box_extracted` — the inner BOX carried out of the outer arm.
* A tag-guarded (call-bound) candidate never takes the nested route: its variant
  is unknown at the drop and the two-level walk has no tag guard of its own.

## Still open

`optret_payload_tag` has no character for a nested release, so a `return` out of
a consuming arm falls back to `#a` — a plain dec of the inner box, which strands
its payload. A leak on that path only, never an over-release, and the same
fallback the comment there already documents for `string[]`.
