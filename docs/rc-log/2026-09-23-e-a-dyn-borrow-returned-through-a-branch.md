# A dyn borrow returned through a branch

2026-09-23. Native. Review of #10101 found the returned-borrow retain
(`2026-09-23-c-…`) keyed on the returned expression's own shape. A borrow
that reached the return through an `if` or `match` arm took no unit, and
handed the caller the argument's cell:

```
function pick(c: boolean, l: dyn Label, m: dyn Label): dyn Label {
    return if (c) { l } else { m };
}
```

On x86-64 `-sanitize` this was a use-after-free. So were
`var r: dyn Label = if … ; return r;` and a literal `match`.

## What changed

- **The alias change reaches it.** An `if` or `match` arm yields through
  `emitCountedYield`, which retains an aliased arm value
  (`needsRcIncOnAlias`). With dyn admitted to `retainsOnAlias`
  (`2026-09-23-d-…`), the branch's value holds a unit of its own whichever
  arm ran. Returned, that unit moves to the caller; bound to a local, the
  local's release pays it.
- **wasm yields the pair.** On wasm a dyn value is the two-word
  `[data, vtable]`. An `if` yielding one opened an `(if (result i32))`
  block, and a `match` stored it into a one-word result slot, so the module
  failed validation ("values remaining on stack", "expected i32"). This
  predates the retain. The `if` now takes the pair block type a two-word
  string uses, and the literal and variant match-expr paths type their
  result slot `dyn`, as the tuple path already did.
- **A variant match with a dyn result frees its fresh scrutinee.**
  `reclaimableMatchScrutinee` refused a pointer-typed result, since an arm
  could yield an uncounted piece of the box. A dyn result is a counted yield
  on every arm (`countedDynResult`), so nothing it holds dangles. Typing the
  slot `dyn` had made this refusal fire and leak the scrutinee box.

## Measured

x86-64 `-sanitize`, three trips over two local records:

| shape | #10072 alone | now |
|---|---|---|
| `return if (…) { l } else { m }` | use-after-free | 15 / 15, 0 live |
| `var r = if …; return r` | use-after-free | 15 / 15, 0 live |
| `return match (c) { 0 => l, _ => m }` | use-after-free | 15 / 15, 0 live |
| `return match (tag(c)) { First => l, … }` | 16 B live once the slot was typed | 16 / 16, 0 live |

All four answer 23 on arm64 and wasm (`TestDynReturnedBorrowThroughABranch`).

## Found on the way

A local that starts as a unit variant and is reassigned to a payload variant
leaks the payload box on native, with or without dyn (#10103). The branch
test builds its scrutinee through a call instead.
