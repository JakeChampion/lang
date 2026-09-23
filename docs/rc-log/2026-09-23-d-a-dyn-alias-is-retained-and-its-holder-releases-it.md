# A dyn alias is retained, and its holder releases it

2026-09-23. Native. A dyn value stored anywhere past a borrow took no unit
of its own. A parameter written into an array element or captured by a
closure copied the caller's `{data, vtable}` cell pointer, so the holder's
release freed what the caller still held. `[l, l]` released one cell twice.
A struct field or tuple element holding a dyn was never released by its
container's drop at all, so a fresh concrete stored there leaked. Closes
#10073 and #10082.

## What changed

- **A dyn alias is retained like every other reference.** `retainsOnAlias`
  admits dyn wherever the backend reclaims it, and `emitAliasInc` routes it
  to `emitDynRetain`. That covers a var from a parameter, an array element,
  a struct field initialiser and a closure capture. It retired
  `dynBorrowedViews` and its sweep, reinit and frame-ownership special
  cases, and the dyn arm of `markConstructionMoves`: a view of a parameter
  is now an ordinary owned local. `dynParamBorrowed` is what is left, for
  the return move.
- **A struct field or tuple element holds a unit of its own, and the drop
  releases it.** `genStructDropFn`, `genTupleDropFn` and the inline
  tuple-local drop share `dynSlotDrop`, on every backend.
- **A copy of a dyn slot takes its own unit.** A struct spread retains a
  copied dyn field (`emitCopiedFieldInc`). On x86-64 it used to apply
  `__fern_rc_inc` to the header-less cell, writing eight bytes before it,
  and the program computed the wrong answer. A destructured binding
  retains what it projects.

## Measured

x86-64 `-sanitize`, 20 trips, against the #10072 build:

| shape | before | after |
|---|---|---|
| `[l, l]` from a parameter | use-after-free | 0 live |
| an escaping closure capturing a parameter | use-after-free | 0 live |
| `let Holder { l } = hold(local)` | use-after-free | 0 live |
| a tuple `(d, 1)` destructured | use-after-free | 0 live |
| `Two { ...w, n: 2 }` over a dyn field | exit 24, want 160; 640 B live | 160, 0 live |
| `Holder { l: Box { … } }` | 640 B live | 0 live |
| `Holder { l: local }` | 640 B live | 0 live |
| `Holder { l: i }` | 640 B live | 0 live |

The same answers hold on arm64 and wasm.

Test coverage, in `internal/e2e/rc_dyn_alias_test.go`:

- `TestDynAliasStoredPastTheBorrow` fails on all three backends without the
  change.
- `TestDynCaptureStoredPastTheBorrow` fails on x86-64 and arm64.
- `TestDynFieldReleasedByStructDrop` fails on x86-64 and arm64.
- The bounded legs fail on x86-64, and the field one also on wasm.

## What it does not reach

- wasm still does not reclaim an enum payload or a closure capture holding
  a dyn (§7.8).
- An escaping dyn-capturing closure dispatches wrongly on wasm (#10075).
- A map value holding a dyn is released by nothing.
