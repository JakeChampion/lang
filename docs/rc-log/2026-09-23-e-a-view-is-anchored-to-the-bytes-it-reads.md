# A view is anchored to the bytes it reads

2026-09-23. Self-host typed path. The last two corpus cases the AST lowering
held both return a view: `alloc_flat_method_identity_return`'s `tail` a `str`
of its receiver, and `string_slice_option`'s `first_three` the `Option[str]`
a checked slice builds. Both now produce whole, 62 of 62 and 3 of 3. Every
case in the corpus now produces on the typed path.

Getting there exposed a miscompile the typed path already had on main. A
checked slice wraps its view in a fresh `Some`. A construction is not a
projection, so nothing tied the `Option` to the string it reads, and the
string was released at its own last use, the slice:

```
var s: string = "abcdefghijklmnopqrstuvwxyz0123456789" + i.to_string();
match (s[0:30]) { Some(v) => { var junk = ...; print(v); }, ... }
```

On main this prints `junk`'s bytes where `v`'s should be.

## What changed

- **A value that gathers views is anchored to their bytes.**
  - `ssasem.bytes_root` follows each view to the value holding its bytes:
    through projections, and to the one source a construction's or a phi's
    operands share.
  - `borrow_parents` makes that source the parent of the construction or phi,
    so the source stays alive until the last read.
  - A value gathering views of two sources has no single anchor, and its
    function is refused.
  - An operand whose box is a counted string's own anchors nothing: a string
    retagged, a literal, or a phi of those. Its holder's unit carries the
    bytes. That is `examples/cli/fold.fern`'s `rest`, a `str` that starts as
    the parameter and is reassigned from `std/string`'s `drop`, which returns
    an owned string. It now produces whole, 71 of 71.
- **A view result is anchored to a parameter.**
  - `semsource.anchor_module` finds the one parameter every returned view
    reads (`ssasem.result_anchor`). It runs to a fixpoint, because an anchor
    passes through a callee's.
  - Every body carries the table (`ssasem.Anchor`), and a caller anchors the
    call's result to that argument. That is what keeps a temporary receiver
    alive past the call.
  - A result reading a local is the checker's E065. One reading either of two
    parameters is refused ("view result escapes its source").
  - The ownership inference keeps the parameter lent: a returned view reads it
    without taking its unit.
- **A retagged string may be retained.** A `str` that is a `string` retagged
  is the string's own counted box, so its retain is the string's. That covers
  `tail`'s `return s`.
- **A view payload moves out of a box nobody else sees.**
  - The payload take extends to a view, and to the builtin Option layout, when
    the box is a construction (or a phi of them) that only tag tests and
    payload reads ever touch (`sole_box`). Its count is 1 at the read, so
    there is nothing to test.
  - Option has no store to null the slot with, so the box's drop in that step
    releases the box alone (`shell_root`).
  - The flow is derived again for such a take, and every chain through the
    taken value stops at it. Otherwise a projection of the payload kept the
    box alive past the read, and the box's full drop released the payload a
    second time.

## Measured

x86-64, `FERN_SANITIZE=1 FERN_LEAKCHECK=1`, typed against the AST lowering:

| program | typed | AST |
|---|---|---|
| `alloc_flat_method_identity_return` | 1354 / 1354, 0 live | 1202 of 1204 freed, 64 B live |
| `string_slice_option` | 50 / 50, 0 live | 16 of 49 freed, 1104 B live |
| slice of a dying local, 3 trips | 24 / 24, 0 live, right bytes | 15 of 24 freed, 384 B live |
| `tail` of a temporary receiver, 3 trips | 41 / 41, 0 live | 33 of 41 freed, 312 B live |

The self-host compiler's own source still produces whole, 8790 of 8790.

Production rows (`TestSelfHostSemanticProduction`), all four legs:

- `a-view-result-is-anchored-to-its-argument`: fails on x86-64 and arm64
  without the caller's anchor.
- `a-slice-view-keeps-its-local-alive`: fails on main.
- `an-option-view-result-takes-its-payload`
- `a-view-result-of-either-parameter-is-refused`
- `a-view-of-counted-strings-carries-its-own-bytes`

## Not reached

A loop-carried view whose source is made inside the loop has a root that does
not dominate the loop's phi. Its function fails the dependency check and is
refused, where before it was produced with a view that outlived its source.
