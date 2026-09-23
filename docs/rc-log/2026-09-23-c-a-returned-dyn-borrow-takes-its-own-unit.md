# A returned dyn borrow takes its own unit

2026-09-23. Native. A function returning a dyn value it held only as a
borrow handed the caller back the very `{data, vtable}` cell the caller had
built for the argument, with no retain:

```
function pass(l: dyn Label): dyn Label { return l; }
```

Two holders then shared one cell that nothing counted, so the second release
freed the concrete under the first: x86-64 `-sanitize` reported a
use-after-free, and the plain build segfaulted. Closes #10072.

## What changed

- **Every concrete behind a dyn is counted.** `boxPrimitiveDynValue` gives
  a primitive's value box an rc header, and `__drop_dynprim_<prim>` frees
  it at the last unit and decrements otherwise. A struct or enum box
  already had one. That is what makes a retain possible without the
  concrete's static type, which a dyn parameter does not have.
- **A borrowed dyn value takes a unit of its own when it is returned.**
  `dynBorrowed` names a dyn parameter (not `own`), a view of another
  holder's value (`dynBorrowedViews`), and a field, element or capture
  read. `emitDynRetain` applies the flat rc inc to `data`, and on the
  natives builds the holder a cell of its own. The move-on-return for a
  dyn local now applies only to one the frame owns.

The model this follows is recorded in DYN-TRAITS.md §4.5.

## Measured

x86-64 `-sanitize`, 20 trips, against the #10069 build:

| shape | before | after |
|---|---|---|
| `pass(local)` | use-after-free | 84, 0 live |
| `pass(Box { … })` | use-after-free | 164, 0 live |
| `viaview(local)`, returning a local that views a parameter | use-after-free | 80, 0 live |

A primitive coerced at a dyn argument stays balanced (190, 0 live): the box is
counted now and released by its drop, where #10069 freed it outright. The
same answers hold on arm64 and wasm.

Test coverage:

- `TestDynReturnedBorrowTakesItsOwnUnit` fails on all three backends without
  the change.
- `TestDynReturnedBorrowBounded` grows on x86-64 without it.

## What it does not reach

A borrowed dyn value stored into a field or an array element still copies
the cell pointer with no retain, and use-after-frees the same way (#10073).
That fix routes dyn through `rcIncOnAliasType` and `emitAliasInc` and
retires `dynBorrowedViews`; it feeds the ownership analysis widely enough
to be its own change.
