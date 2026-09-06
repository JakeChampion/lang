# 2026-09-06 — an f-string RHS was invisible to the ownership classifiers (#8697)

Found while confirming #8441's control case, which that issue quotes as clean
and is not. The leak has nothing to do with the capture box:

```fern
import "std/i32";
function main(): i32 {
  var s: string = "";
  var i: i32 = 0;
  while (i < 1000) { s = f"{i}-iteration"; i = i + 1; }
  return 0;
}
```

| target | allocs | frees | live bytes |
|---|---|---|---|
| x86-64-linux | 2000 | 1000 | 32000 |
| wasm32-wasi | 2000 | 1000 | 32000 |
| arm64-linux | 2000 | 2000 | 0 |

One 32-byte block per store. The hand-written `s = i.to_string() +
"-iteration"` beside it is clean on all three.

## Cause

The checker desugars an f-string into a `+`-chain on `FString.Desugared`, and
`ir.go`'s `*ast.FString` case lowers Desugared alone — `Parts` survives only so
the formatter can rebuild the surface syntax. Every ownership classifier in
`internal/ir` switches on the raw node, and none had an `*ast.FString` arm, so
an f-string RHS reached each one's conservative default:

- `rhsTainted` answered "tainted", so the destination local never became
  `freeEligible` and the dec-on-overwrite was suppressed. That is the leak.
- `freshOwnedRcTempType` declined it, so an f-string in argument or
  discarded-statement position was not the fresh owned temp it is.
- `isOwnedStringTemp` answered "no", so a borrowing consumer did not know the
  value arrives owning its reference.
- `exprNoParamEscape` rejected it, although a concat byte-copies both operands
  and can carry no parameter heap — the fact its own `*ast.Binary` arm states.

Shape decides whether it bites: `f"{i}"` desugars to a bare `to_string()` with
no concat and never leaked, so the literal tail is load-bearing.

arm64 is clean because its overwrite path does not consult `freeEligible` the
same way. The classifier gap itself is target-independent.

## Fix

`unwrapFString` at the head of the four classifiers, so each reads the node the
codegen will lower.

Left alone deliberately: `exprSafeToReevaluate` and `cheapToDuplicate` already
answer false for an f-string, which is the right conservative answer for an
allocating concat, and `isSelfStrAppendLocal` declining `s = f"{s}…"` costs an
in-place-append optimisation rather than correctness.

## Gate

`TestFStringLowersLikeItsDesugaring` (`internal/ir`) lowers each shape both
ways and compares whole op streams — the gap was four classifiers wide, so
pinning the property rather than the one missing `__fern_str_dec` is what keeps
a fifth from drifting. All four cases fail on the pre-fix compiler.

`k` is read after the loop in those fixtures on purpose: left dead there the
two spellings differ legitimately, the f-string form getting an early
post-loop drop of the receiver the concat form does not.

Plus the `fstring_reassign_releases_old` conformance fixture with a census row
of 0, and the `fstring_reassign_releases_superseded` rc corpus case, which
holds 0 on all three backends.
