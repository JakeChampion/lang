# `T[] -> T` generics silently miscompile on the self-host x86-64 backend

**Status:** open bug, characterised. No fix in this note.
**Severity:** silent wrong values — compiler exits 0, no diagnostic.

## Reproducer

```fern
function first_of[T](xs: T[]): T { return xs[0]; }
function main(): i32 {
    var xs: f64[] = [4.5, 1.5];
    return (first_of(xs) * 10.0) as i32;   // want 45; self-host x86-64 gives 255
}
```

## Measured

| element type | interp | native x86-64 | **self-host x86-64** | self-host wasm |
|---|---|---|---|---|
| `i32` | 45 | 45 | 45 | 45 |
| `f64` | 45 | 45 | **255** | refused |
| `i64` | 9 | 9 | **0** | refused |
| `string` | 45 | 45 | **40** (`len()` → 0) | 45 |
| struct | 45 | 45 | refused | refused |

Only `i32` is correct. Struct elements are correctly *refused*. The other three
are silent wrong answers.

## This is NOT the erased-wide stride problem

`docs/SELFHOST-ERASED-WIDE-ARRAY-GENERICS.md` is about an erased ELEMENT WIDTH
producing a 4-vs-8-byte stride, which is why it was wasm-only — on the register
backends every slot is 8 bytes, so the stride is harmless.

That reasoning does not cover this case, and I had been leaning on it:

- **`string` breaks here.** A string is a pointer, not a wide scalar; its width
  is not in question on either backend. `len()` came back **0**, i.e. the
  returned value was not a valid string at all.
- **It is x86-64, not wasm.** The wasm leg either refuses (i64/f64, via the
  #6250 gate) or is correct (string).

So "register slots are 8 bytes, therefore erasure is harmless" is true of the
element *stride* and false of the erased *return*.

## What isolates it

Two controls place the fault precisely at "read an element out of an erased
array and return it as an erased value":

- `count_of[T](xs: T[]): i32 { return xs.len(); }` — erased array param,
  **non-erased return** → correct on every path.
- `id_of[T](x: T): T { return x; }` — **bare** `T` param and return, no array →
  correct on every path (this is the #5586 pass-through shape, already handled).

Neither the erased param alone nor the erased return alone is broken. The
combination is.

## A self-inflicted coverage note

While measuring the above, `count_of[T](xs: T[]): i32` at an `f64[]` came back
**refused on wasm**. That is the #6250 gate being wider than it needs to be: the
callee never touches an element, so no stride is ever used, and refusing it buys
nothing.

The gate keys on "a wide-element array reaches an erased `T[]` param" without
asking whether the element is ever read. Tightening it — refuse only when the
erased element actually escapes — is a real improvement, but it is exactly the
kind of plausible-looking narrowing that wants a measurement first rather than a
confident edit. Recorded here rather than fixed on the spot.
