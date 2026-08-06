# `T[] -> T` generics silently miscompile on the self-host x86-64 backend

**Status:** FIXED. Clause (c-arr) in `parse_func` now promotes on ANY mention of
the type var in the return, so `first_of[T](xs: T[]): T` monomorphises per
concrete element type. Every row in the table below is now the oracle's value on
every path. One shape remains — see the last section.
**Severity when found:** silent wrong values — compiler exits 0, no diagnostic.

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

Only `i32` was correct. Struct elements were correctly *refused*. The other
three were silent wrong answers.

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

## Root cause

It is not the value's width — it is that the CALL SITE does not know what came
back. Compare the two `.len()` lowerings the self-host x86-64 backend emitted
for a `string[]`:

```
    movq 8(%rax), %rax      # xs[0].len()        — string box, length at +8
    movq (%rax), %rax       # first_of(xs).len() — ARRAY box, length at +0
```

The callee is fine: it loads the element at an 8-byte stride and returns it in
`%rax`. The caller then dispatches `.len()` on an erased `T` result and falls
back to the array receiver, so it reads the wrong slot of a perfectly good
string box and gets 0. `f64` and `i64` go wrong for the mirror-image reason —
the result is moved and operated on as the wrong kind of value.

## Fix

Clause (c-arr) in `parse_func` already promoted a single-typevar generic with a
bare `T[]` param whose return *feeds a wide container* (`reverse[T](xs: T[]):
T[]`, #6238). It now also fires when the return IS the bare typevar, so
`first_of` is monomorphised to `first_of__f64(xs: f64[]): f64` and the call site
has a concrete result type. That fixes all three broken widths at once, and it
also makes the struct case — previously refused on both self-host backends —
lower and run.

Nothing in the stdlib or the compiler matches `T[] -> T` (a scan found zero), so
the bootstrap monomorphises nothing new and byte-identity holds. This is the
same argument clauses (c′) and (c″) rest on.

Pinned by `TestSelfHostErasedElemReturn{X86_64,Wasm}`, which assert VALUES
against the interp oracle rather than routing — every case here routed `ir`,
exited 0, and reported nothing under `FERN_STRICT_IR=1`.

## Follow-up: the gate was wider than it needed to be

`count_of[T](xs: T[]): i32` at an `f64[]` was still **refused on wasm** after the
promotion above — which does not reach it, the return being concrete. The #6250
gate keyed on "a wide-element array reaches an erased `T[]` param" without asking
whether the element is ever read. This callee never touches one, so no stride is
ever used and refusing it bought nothing.

Measured before narrowing rather than assumed: of the 47 single-typevar
`T[]`-param generics in `std/array`, exactly **one** is element-blind —
`is_empty[T](xs: T[]): boolean { return xs.len() == 0; }`. All the other
concrete-return shapes (`all`, `any`, `count_where`, `position`, `sum_by`, …)
feed elements to a predicate and are genuinely width-sensitive; the gate is right
to refuse those.

So flag `'7'` is now emitted only when `elem_read_in_body` says the body reads an
element. That predicate is deliberately lopsided: it answers YES for every use of
the param except one — as the receiver of `.len()`, which reads the header word
and nothing else. A bare mention anywhere else, including a reassignment target,
counts as a read. Too permissive here reopens a silent-miscompile class, so an
unrecognised use is a read.

Both edges are pinned, and neither test alone is enough:
`TestSelfHostErasedWideArrayGateWasm` fails if the gate gets too narrow (the
two-typevar `array.map` at `f64[]` must still refuse),
`TestSelfHostErasedWideArrayGateBlindWasm` if it gets too wide.

## The same defect had two more return spellings

`T[] -> T` was not the last of it. Two more shapes were still silently wrong on
the self-host x86-64 backend after the bare-typevar case landed:

| shape | before | after |
|---|---|---|
| `pair_of[T](xs: T[]): (T, T)` at `f64[]` | 0 (want 60) | 60 |
| `pair_of[T](xs: T[]): (T, T)` at `string[]` | 42 (want 45) | 45 |
| `head_and_one[T](xs: T[]): (T, i32)` at `f64[]` | 0 (want 45) | 45 |

Which is the point: the clause had grown from `feeds_wide_container` to
`feeds_wide_container || ret_type == un`, and a tuple return fell through both.
Fixing the spellings one at a time is how a defect gets fixed twice and stays
open. So the condition now asks the question that actually matters —
`type_mentions_var(ret_type, un)`, i.e. does the call site have to know what came
back — and the two-branch spelling test is gone.

## Still open: a monomorphised clone's call site reads the PRE-clone type

`array.enumerate[T](xs: T[]): (i32, T)[]` at an `f64[]` is still wrong (255 on
self-host x86-64, 0 on wasm). The promotion fires and the clone is correct —
`__fn_enum2__f64` is in the emitted asm — but the CALL SITE is not:

```fern
function enum2[T](xs: T[]): (i32, T)[] { … }
var ps = enum2(xs);      // ps[0]'s stamped type is "(i32, T)", not "(i32, f64)"
var (i, v) = ps[0];      // T is not a width, so v reads as i32
```

`checker.annotate_module` runs before `monomorphize_module`, so the `ty` stamped
on `ps[0]` carries the template's spelling with the type var still in it. That is
the same class as #6181 (monomorphisation rebuilding nodes and dropping `ty`),
one step further out: here the tag survives the rebuild and is simply the wrong
answer for the clone. Fixing it means re-annotating clones after
monomorphisation, which is bigger than any of the changes above and wants its own
diff.
