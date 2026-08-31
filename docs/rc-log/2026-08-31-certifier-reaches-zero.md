# The certifier reaches zero (2026-08-31)

Five slices took the static leak walk from a probe reporting 20.3% of
functions in census-clean fixtures to **0.00% — 0 functions, 0 values,
727 functions over 323 fixtures the runtime proves clean.**

The gate stops being a ratchet on a rate and becomes the property Roc's
`arc_certify.zig` has: the walk may make no claim the runtime
contradicts. A finding now fails the build.

## The five causes, and that none was filtered

| class | cause | slice |
| --- | --- | --- |
| `enum_sentinel` | a static `.rodata` cell read as a unit | unit-holder set |
| `alloc`, `call` | a unit threaded through a loop is disposed of under the PHI's name | phi transfer |
| `make_closure` | `ir.OpConstFunc` lifts to `OpMakeClosure`, and that cell is `.rodata` too | static-cell stamp |
| `transferred` | the impossible fall-through of an exhaustive match | #7848 |

Four of the five are one mistake wearing different clothes: **an address
that cannot be freed, counted as a unit.** The fifth is its mirror — a
unit that can be freed, under a name the walk was not watching. The
last is not an analysis defect at all.

## What #7848 actually was

`internal/ir` has no `unreachable` op. An exhaustive match still tested
its last arm's tag, so the arm where the tag matched no variant fell
through to a fabricated default:

```
enum List { Cons(i32, List), Nil }        // two variants, both matched

block 3:  brif tag == 1, block 10, block 11
block 11: br block 9  →  block 9: br block 2  →  block 2: ret const 0
```

For an address return that `const 0` is a null `List` — a shape the type
admits no instance of, which a corrupted tag would hand to a caller to
segfault on later. For an `i32` return it is a valid integer and a quiet
wrong answer, which is worse.

The consumed parameter was released on every arm that can execute and
not on the one that cannot, so the certifier was right about the op
stream and wrong about the program.

**The fix removes the arm rather than exempting it.** The checker proves
exhaustiveness and now stamps the arm that covers the remainder;
lowering emits that arm with no tag test, the way the unguarded wildcard
and the all-binder tuple arm already do. Zero findings is then reachable
by construction, and the certifier never has to special-case its own
compiler's filler — which is the whole point, because a certifier that
does is not one anything can be gated on.

## The two mistakes this cost, both mine

Worth recording beside the result, because the area's own rule (#7787:
*no aggregate is publishable until one instance has been read end to
end*) is what caught both.

**I nearly filed a lowering bug that did not exist.** `make_closure`
read exactly like a leak — a closure allocated, handed to a borrowing
callee, a drop emitted against a null operand. The runtime said 0
unpaired allocations. The value was never a heap block.

**I mis-scoped the residue as 6 of 7 plus an outlier.** The seventh was
the same shape; my detector keyed on `ReturnAddr`, and `eat` returns
`i32`, so its fall-through `ret const 0` is type-valid and slipped
through. Correcting it strengthened the fix — the narrow option now
covers all 7 rather than 6 — and it changed what the defect *is*:
emitting the unreachable arm, not returning a null.

## What zero does not mean

- **Corpus-bounded.** 323 conformance fixtures on x86-64, not the
  self-host compiler and not `examples/`.
- **Path-bounded.** The census observes the path each fixture takes. A
  leak on an untaken path is invisible to it, so agreement is not proof.
- **Coverage-bounded, and this is the live number.** 360 functions still
  fail to lift (#7803, all value-typed `OpBlock` after the pass
  battery), and 456 call results are still `UnitUnknown` — defined
  callees whose returns phase B could not place. The floors in the gate
  are what keep those honest; a walk that understood nothing would
  report zero too.

## Next

`Certify` runs in a corpus test, not on the compile path. Roc's
certifier runs in the compiler. Moving it there needs a package that
imports both `internal/ir` and `internal/ssa` — `internal/ssa` imports
`internal/ir`, so `ir.Verify` cannot call it — which is a structural
change rather than a wiring one.
