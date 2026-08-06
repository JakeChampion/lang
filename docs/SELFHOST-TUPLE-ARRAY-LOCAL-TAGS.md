# A tuple-array local bound from a call loses its element tags

**Status:** open, sites identified and measured. No fix in this note.
**Severity:** silent wrong values on wasm — compiler exits 0, no diagnostic.

## Reproducer

```fern
function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 {
    var ps = mk();
    var t = ps[0];
    return (t.1 * 10.0) as i32;   // want 45; self-host wasm gives 1
}
```

## Measured

| probe | form | interp | self-host x86-64 | self-host wasm |
|---|---|---|---|---|
| w1 | `var ps = mk(); var t = ps[0]; t.1` | 45 | 45 | **1** |
| w5 | w1 then `var (a, b) = t` | 45 | 45 | **0** |
| w6 | `var ps = mk(); for p in ps { … p.1 }` | 45 | 45 | **1** |
| w2 | `var ps = mk(); ps[0].1` — no intermediate local | 45 | 45 | 45 |
| w3 | `var ps: (i32, f64)[] = mk(); var t = ps[0]` | 45 | 45 | 45 |
| w4 | array literal, annotated | 45 | 45 | 45 |
| w7 | annotated + `for p in ps` | 45 | 45 | 45 |
| w8 | w1 but a `string` element | 45 | 45 | 45 |

Two things fall out of the table:

- **The intermediate binding is what breaks it.** The direct read (w2) is fine;
  binding `t` or a loop variable from the same index is not.
- **It is width-specific, which is why it is wasm-only.** An untyped element
  reads as a 4-byte i32. A string is a 4-byte pointer, so w8 survives; an f64 is
  8 bytes, so w1 does not. On the register backends every slot is 8 bytes and
  nothing is lost.

## The sites

Three places in `irlower.fern` read a TUPLE element tag off an array slot's
`arrarr_elem_of_slot`, and they are siblings by construction:

| line | site | `ExprIndex.ty` fallback |
|---|---|---|
| 1189 | `expr_tuple_elem_tag`'s `ExprIndex` arm | yes — #6165 |
| 14728 | `var t = ps[0]` — tuple-local binding | **no** |
| 15613 | `var (a, b) = ps[0]` — destructure | yes — #6279 |

`arrarr_elem` is only recorded for an ANNOTATED `(tuple)[]` binding, so
`var ps = mk();` leaves it empty and the un-guarded sites fall through to the
untyped read.

A fourth site has the same gap but no `ExprIndex` to fall back on:

| line | site |
|---|---|
| 17712 | `for p in <(tuple)[]>` — the loop variable |

## The lesson this is a second instance of

`docs/TYPED-IR-REWRITE.md` gained a rule after #6279:

> When wiring a carrier into a consumer, grep for the walk it replaces — here
> `arrarr_elem_of_slot` — and fix every reader of it in the same diff.

That rule was written having fixed two of the four readers, and it did not get
applied to the other two. Writing the rule down is not the same as running the
grep; the grep is three seconds and it is what actually finds the siblings.

## Fix direction

Two shapes, and the second is better if it can be made to work:

1. **Per-consumer**, the proven pattern: add the `ctix.ty` fallback at 14728,
   matching 1189 and 15613. Small and certain, but leaves 17712 (no ExprIndex
   node) and any future reader.
2. **Upstream**: record `arrarr_elem` when an array local is bound from a call,
   so all four sites — and future ones — read a populated slot. This is the
   fix that actually retires the class.

(2) needs the call's ARRAY ELEMENT type, which `ExprCall.ty` does not carry:
`type_to_irtag` has no `TypeArray` arm by design, because its result is read by
tag-FIRST consumers that short-circuit on any non-empty `c.ty` (that is why
`type_to_arrtag` exists separately for `ExprSlice.ty`). So (2) needs either a
separate carrier or a registry lookup of the callee's declared return type —
scope it before starting.
