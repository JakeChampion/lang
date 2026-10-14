# 2026-10-14 — a with on a borrowed array is a link

`compiler/ssaunits.fern`: `deferred_retain`, `links_of`, `grow_rows`,
`carried_params`, `lent_withs`. `compiler/ssarc.fern`: `with_update`,
`with_linked`, `borrowed_with_base`. Fixes #12332. Changes emitted code.
Needs 2026-10-14-a (a dispatched call is bracketed).

## The shape

```fern
function swap(xs: i32[]): i32[] {
    let first: i32 = xs[0];
    let last: i32 = xs[1];
    xs = xs.with(0, last);
    xs = xs.with(1, first);
    return xs;
}
```

`xs` is borrowed. The first `with` took a unit of it with a retain before its
count test, so the test always read two counts and always copied, however
many holders the array really had. `a = swap(a)` run 100 times allocated 100
times.

An append through a borrowed parameter already avoids this: its retain is
deferred so the count test reads the caller's units, the result is a link
that owes its unit while it is still the parameter's box, and every caller
still reading the array brackets the call. `.with` was left out of all three
parts.

## What changed

- `deferred_retain` defers a with's retain on the terms an append's has: a
  borrowed parameter the frame reads no further.
- `links_of` takes a with onto the parameter or onto a link as a link, and
  a with's receiver as a place a link may be moved.
- `ssarc.borrowed_with_base` writes the parameter's box in place when its
  count test finds it sole, retaining it for the unit the result owes unless
  the result is a link, and copies it otherwise. `with_linked` takes that
  path while the link is still the parameter's box, and the owned path once
  it is a box of the frame's own.
- `grow_rows` gives a with the rows an append has, and `carried_params`
  follows a with's receiver, so a caller still reading the array brackets
  the call.
- `sole_boxes` no longer takes a with's result as sole when the receiver is
  carried from a borrowed parameter the frame reads no further
  (`lent_withs`): that result may be the caller's box.

## Measured

`a = swap(a)` 100 times on a two-element array, then once with a retained
alias, x86-64 `-O`, counted with `__heap_alloc_count()`:

| | unique chain | shared |
|---|--:|--:|
| before | 100 | 1 |
| after | 0 | 1 |

The same holds on arm64 and wasm (`TestSelfHostBorrowedWithChainAllocs`),
including a `string[]` chain, a loop through a phi, and a wrapper calling
`swap` twice.

## Tests

- `TestSelfHostBorrowedWithChainAllocs`: the counts above, on three targets.
- `TestSelfHostBorrowedWithChain{X86_64,Arm64,Wasm}`: under the leak census,
  and the sanitizer on x86-64. The rows cover scalar, string and nested
  arrays, a loop, a caller's retained alias, a wrapper, a call through a
  function value, and a callee reading its parameter after updating it.
  Each must keep the old value.

## The trap

Running the function-value case first found #12373. A call through a
function value was never bracketed, so a write in place there rewrote a
caller's retained alias. Appends had the same hole on main.
