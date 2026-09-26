# 2026-09-26 — a counted copy of a local owns its count (#9948)

`chunk = kept`, with `kept` a borrowed alias whose inc #9923 cancelled, left
`chunk` inheriting kept's borrow taint through `rhsTainted`'s Ident arm. So
`chunk` was not `freeEligible` and every drop of it fell back to the flat
`__fern_rc_dec`, including the one for its own `[]` initialiser on the rounds
that never reach the alias. 50 rounds: `allocs=250 frees=200 live_bytes=800`
on arm64-darwin and wasm alike.

## Why no second pass is needed

The issue read this as circular: `computeFreeEligible` runs before
`computeBorrowedAliases`, and the borrowed-alias legs read `freeEligible`.
The verdict chunk needs does not depend on which locals end up cancelled,
though. The Assign lowering retains an owned-local source unless the site is
a move, and a move source hands over a count it OWNS. The one local that holds
no count is a cancelled borrowed alias, and all four borrowed-alias legs
refuse a name in `movedLocals`. So `L = <owned local>` gives L a count of its
own on every path. That is the same argument `countedBindingAlias` already
made for a binding source, and it is now one predicate, `countedIdentAssign`.

What L still inherits is the source's ESCAPE. A source an uncounted sink holds
(a struct map key here) is released by the exit sweep's flat dec, and L's
is_unique-gated drop would then free it under the sink. The fixpoint now
carries `escaped` forward across a counted assign as well as backward. That
clause is load-bearing: with it removed, `escaped_source_keeps_its_taint`
(the map is returned and its key read back in the caller) reports
`fern-sanitizer: use-after-free` on arm64-darwin.

## Measured

`FERN_LEAKCHECK=1`, wasm, `internal/e2e/rc_counted_ident_assign_test.go`:

| case | main | after |
|---|---|---|
| `cancelled_alias_copy` (the issue) | 250 / 200 / 800 B | balanced |
| `copy_handed_out` | 250 / 210 / 640 B | balanced |
| `copy_pushed_past_the_loop` | 104 / 88 / 256 B | balanced |
| `copy_then_reassigned` | 290 / 200 / 2080 B | balanced |
| `copy_of_a_borrowed_parameter` | 100 / 50 / 800 B | balanced |
| `escaped_source_keeps_its_taint` | 1280 B | 1280 B, no over-release |

The last row leaks by design: the map holds the key uncounted.
