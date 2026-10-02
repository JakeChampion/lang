# An alias of a borrowed array gives its reference back on a grow

2026-09-27 — `irlower`: `append_target_sole_owner`, `settle_returned_append`.
Fixes #10367.

```fern
function walk(n: i32, acc: string[]): string[] {
    let out: string[] = acc;
    if (n % 4 == 0) { out = out.append("w" + ""); }
    return out;
}
// main: pending = walk(fd, pending), twelve times
```

On the AST lowering this leaked three buffers (allocs 4, frees 1). The same
program without the local, or with `walk` produced, was balanced.

## Two causes

- `let out = acc` retains acc's buffer, so `out` holds a counted reference to
  a buffer the caller also holds. `append_target_sole_owner` did not ask
  `borrowed_names` (the ownership set `.with` already consults), so the
  self-append took `__fern_arr_push_owned`. On a grow that helper frees the
  old buffer only at rc 1 and otherwise leaves it alone, so out's reference
  leaked. The target now takes the plain push. Its store releases the old
  reference and first retains the elements the copy shares with a surviving
  holder (`emit_aliased_push_store`).
- `return out.append(v)` keeps `out` from the exit sweep
  (`returned_moved_arr_slots`), as if the result were out's buffer. After a
  grow or a copy it is not, and out's reference to the old buffer was never
  released. This held for any counted array local, not only an alias:
  `let a: i32[] = [n, 2, 3, 4]; return a.append(5);` leaked the four-element
  buffer on every call. `settle_returned_append` now stores the result through
  the same aliased-push store, so the kept slot holds the returned reference.

## Measured

`TestSelfHostBorrowedAliasAppend*` has seven rows on x86-64 (leakcheck and
sanitizer), arm64 and wasm, each on the semantic and AST lowerings. Six
balance and failed on main. `struct_elems` balances its buffers now, but its
`Inst` boxes stay behind main's shallow release of a threaded accumulator
(#10420), so it is held to the sanitizer only.
