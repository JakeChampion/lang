# Multidimensional arrays: shape, strides and offset

Status: policy doc, normative where marked. Phase 3 of #9727 (#9734); the
module is `std/ndarray`.

Upstream: `docs/ARRAY-ALGEBRA.md` (what the algebra may do; §4 for shape
mismatch, §5 for views, §6 for what the compiler recognizes),
`docs/STR-VIEW-CONTRACT.md` §5 (what a view is), `docs/REUSE-CONTRACT.md`
(how storage is reused), `docs/ARRAY-BOUNDS.md` (what an out-of-range
access does). This document answers the five questions #9734 lists and
records the materialization rule the non-goals of #9727 require.

## 1. The representation: a counted handle, not a view

**Normative.** A multidimensional array is a value that OWNS a reference
to its storage:

```
NdArray[T] { data: T[], shape: i32[], strides: i32[], offset: i32 }
```

`strides` are in elements and may be negative; element `idx` is
`data[offset + sum(idx[k] * strides[k])]`. Two handles may share one
`data`, and each holds its own count on it.

This is the first decision because `ARRAY-ALGEBRA.md` §5 makes it a
precondition: a `[T]` view holds no count and cannot outlive its frame, so
"a buffer reused under `own` while a view of it is still live" is
impossible today only because a view cannot escape. A transpose or a slice
is stored, returned and passed on, so it cannot be a view under that rule.
`STR-VIEW-CONTRACT.md` §5 then closes the other door: making views retain
their buffer (O3) is refused, for `str` and `[u8]` alike. What remains is
the model Go and NumPy use, stated in Fern's terms: the thing that shares
storage is an owner with a count.

Two consequences:

- **The `[T]` view type is not generalized** (the fourth question of
  #9734). It stays what §5 left it, a non-escaping argument-position
  borrow, and no multidimensional view exists. A rank-1 `NdArray` is not a
  `[T]`; `select` and `slice` produce handles.
- **Reuse needs no new soundness rule.** A handle's `data` is an ordinary
  `T[]` with an ordinary count, so the guards R6 and R7 already run —
  `__fern_rc_is_unique` on the buffer — see every alias, because every
  alias holds a count. An in-place elementwise operation through a handle
  (phase 4) is licensed when the handle is consumed AND its storage is
  unique, and the second half is a run-time fact the existing guard
  answers. The "small view pins large buffer" retention §5 warns of is real
  here and is the price: a `select` keeps its whole storage alive.

## 2. Which operations are metadata and which materialize

**Normative.** The list is closed; an operation not on it is not a
structural operation of this module.

| operation | result | storage |
| --- | --- | --- |
| `from_flat(data, shape)` | row-major handle over `data` | shares `data` |
| `transpose()` | axes reversed | same, no element moves |
| `permute(axes)` | axes reordered | same |
| `reverse(axis)` | stride negated, offset to the old last | same |
| `slice(axis, lo, hi)` | extent narrowed, offset advanced | same |
| `select(axis, i)` | one rank less, offset advanced | same |
| `broadcast_to(shape)` | stretched axes at stride 0, leading axes added (§8) | same |
| `reshape(shape)` | same reading order, new shape | same when `is_row_major()`, else a packed copy |
| `packed()` | the same array, packed | itself when `is_packed()`, else a copy |
| `to_flat()` | the reading order as a `T[]` | `data` itself when `is_packed()`, else a copy |

Two predicates decide the last three rows, and both are cheap reads of the
metadata:

- **`is_row_major()`**: the strides are the row-major strides of the
  shape (an axis of extent 1 has no neighbour, so its stride does not
  count). This is what `reshape` needs: the reading order of the elements
  is then the storage order from `offset`, so a new shape is a new pair of
  `shape`/`strides` over the same `offset`.
- **`is_packed()`**: row-major, `offset == 0`, and `data.len()` equals the
  element count — `data` IS the reading order.

## 3. The materialization rule

**Normative.** A structural operation copies elements only when the table
in §2 says so, and then copies exactly once into packed row-major storage.
`reshape` is the one operation whose cost depends on what came before it;
a caller who needs the guarantee asks `is_row_major()` first. There are no
other hidden copies: `transpose().transpose()` is two handles and no
elements moved, a `select` of a `transpose` is a column with no elements
moved.

This is #9727's non-goal — "no hidden copies in structural operations
without a documented materialization rule" — discharged by writing the
rule down where the operations are, and by holding each row to it with the
allocation counters in `internal/e2e/ndarray_test.go`: a metadata
operation moves under 1 KiB against a 32 KiB element buffer, and a copying
one moves at least the buffer. `ALLOCATION-OBSERVABLE.md` says why the
byte counter can be fooled by recycling; the gate keeps every handle live
so nothing is recycled under it.

## 4. Shapes are runtime values

**Normative.** `shape` is an `i32[]` computed at run time. The rank is a
property of the value, not of the type; no dimension is ever stated
statically, and there is no type-level shape language. `Array[f32, [m,
n]]` in the design note was a sketch, and #9734 already says static shape
information is added only where it earns its complexity. Nothing in
phases 3 and 4 does: `ndarray_test.go` builds every shape from a loop
counter, which is the acceptance line "a program that never states a
dimension statically compiles and runs".

What a static shape would buy — a mismatch reported at compile time —
comes back on the table with broadcasting (#9735), where the derived
shapes multiply. It is not decided here and not foreclosed.

## 5. Indexing, slicing, and what a mismatch does

**Normative.**

- There is no new syntax. Indexing is `get(idx)` with a full index;
  slicing is `slice(axis, lo, hi)`; the partial index is `select(axis, i)`
  and yields a handle of one rank less. #9727's non-goal keeps notation
  separate from semantics, and `ARRAY-ALGEBRA.md` §6 keeps the surface
  ordinary receiver syntax the algebra can recognize.
- A full index of the wrong rank, an index or axis out of range, a slice
  outside `0 <= lo <= hi <= extent`, a permutation that is not one, or a
  shape whose element count does not match its storage **aborts** with
  the bounds rule's status (`ARRAY-BOUNDS.md`, exit 134 on the natives).
  These are the derived shapes `ARRAY-ALGEBRA.md` §4 rules on: error, not
  truncation, because a wrong derived shape is a bug and a truncated one
  is a plausible-looking wrong answer. `zip`'s 1-D truncation (`AA-01`)
  is unchanged; it is the written-shape case.

## 6. What the compiler sees

The acceptance line "one representation, used by both the runtime and the
IR, not a stdlib struct the compiler cannot see" is met the way
`ARRAY-ALGEBRA.md` §6 met it for `map`: **recognition is by resolved
stdlib identity**, not by a builtin type. `std/array`'s `map` is ordinary
Fern that the fusion and in-place passes recognize by the callee it lowers
to; `std/ndarray`'s operations are ordinary Fern that phase 4's kernels
will recognize the same way, keyed on the `ndarray__` prefix modload
reserves, with a user's own `NdArray` no more the algebra's than a user's
own `map` is (#9840 made that rule explicit). The struct's layout is
therefore the representation: four fields the IR reads as any struct's,
which is what lets a kernel take `data`, `shape`, `strides` and `offset`
without a second description of them.

`inner` and `outer` are the first shapes it recognizes (#9735, step 3).
`fern -array-report` lists every site under `std/ndarray products`, with
the element functions the site was handed, keyed on the
`__method_ndarray__NdArray_` mangling that only std/ndarray's receiver
methods get. That list is the kernel half's worklist: a site whose
`mul` and `add` are multiplication and addition over `f64` is `dot_f64`
in disguise, and one handed a closure that captures is not. Recognition
only — every site still runs the scalar loop, nothing fuses, nothing
donates, and `internal/ir/ndarray_shapes_test.go` pins that the
recogniser changes no op.

## 7. Elementwise, and along an axis

**Normative.** The first slices of #9735: the operations that read every
element, stated so that a kernel can replace any of them without a caller
noticing. The list is closed the way §2's is. The two products are the
APL ones: `outer` is `f` over every pair, and `inner` contracts the last
axis of the receiver against the first axis of the argument, so the dot
product of two vectors is a rank-0 handle, two matrices give their
product, and a matrix and a vector give a vector. Both operands of
`inner` must have rank at least 1 and the two contracted extents must be
equal; anything else is a derived shape that is wrong and aborts.

| operation | result | order | storage |
| --- | --- | --- | --- |
| `map(f)` | same shape | reading order | one packed buffer of `len()` |
| `zip_with(b, f)` | the shape the two broadcast to (§8) | reading order | one packed buffer of that shape's count |
| `fold_all(init, f)` | a scalar | reading order | none |
| `reduce_axis(axis, init, f)` | `axis` dropped, one rank less | increasing index along `axis` | one packed buffer of `len() / shape[axis]` |
| `scan_axis(axis, init, f)` | same shape, the running fold | increasing index along `axis` | one packed buffer of `len()` |
| `outer(b, f)` | `a.shape() ++ b.shape()`, every pair | reading order of the result | one packed buffer of the result count |
| `inner(b, init, mul, add)` | last axis of `a` against first of `b`: `a.shape()[:-1] ++ b.shape()[1:]` | increasing index along the contracted axis | one packed buffer of the result count |

Three rules follow:

- **Every fold is in increasing index order**, along the axis or through
  the reading order, on a strided handle exactly as on a packed one. This
  is `ARRAY-ALGEBRA.md` §3 (AA-02) carried to the handle: a float
  reduction along an axis means one thing on every backend, and a kernel
  that reassociates it is wrong, not fast. The e2e gate holds it with an
  order-sensitive fold over a transpose.
- **`reduce_axis` is lane-sized.** It walks the input once in reading
  order and keeps one accumulator per lane (the row-major position in the
  shape with `axis` removed), so its allocation is the result, never a
  copy of the input. `internal/e2e/ndarray_test.go` bounds it under the
  element buffer where `map` is bounded above it.
- **A shape mismatch aborts.** `zip_with` over two shapes that do not
  broadcast (§8) is a derived shape that is wrong (`ARRAY-ALGEBRA.md`
  §4), and takes the same status an out-of-range index does (§5).

`map` and `zip_with` allocate their result even when the receiver is
consumed and unique. §1's licence for the in-place form is stated and
unimplemented; taking it is the kernel work, not this list's.

## 8. Broadcasting

**Normative.** Two shapes broadcast when, aligned at their **last**
axes, every pair of extents is equal or one of them is 1; the shorter
shape is padded with leading 1s, and the result takes the larger extent
at each axis. `broadcast_shape(x, y)` computes it and aborts when the
shapes do not broadcast. So a rank-0 handle broadcasts against anything,
a row `[n]` against a matrix `[m, n]`, and a column `[m, 1]` against it
too; `[m, n]` against `[n, m]` (with `m != n`) is an error, and so is a
row of the wrong length. This is the rule NumPy, Julia and Rust's
`ndarray` share, and it is stated here because `ARRAY-ALGEBRA.md` §4
requires the rule to exist before an operation may apply it.

**Broadcasting is a view.** `broadcast_to(shape)` gives a stretched axis
stride 0 and adds leading axes at stride 0, so the one element is read
`n` times and nothing is copied — the same row in §2 as `transpose`.
`zip_with` broadcasts its operands this way, so a row subtracted from a
matrix, a scalar times a matrix, and the outer product of a column and
a row are each one walk and one result buffer.

Four consequences:

- **A broadcast shape is still a shape**: its element count must fit an
  `i32`, because that count indexes storage, and `broadcast_to` to a
  shape whose count does not fit aborts like any other wrong shape even
  though no storage of that size is ever allocated.
- **A stretched handle is not row-major** (unless every stretched axis
  has extent 1 or 0), so `reshape`, `packed()` and `to_flat()` copy it
  into real storage, and the copy has the broadcast count. That is the
  materialization rule of §3 applied, not a new one.
- **A reduction along a stretched axis reads the same element `n`
  times**, in increasing index order like any other; there is nothing to
  special-case.
- **Nothing writes through a stride of 0.** §1's in-place licence needs
  the handle consumed and its storage unique; a broadcast handle shares
  its storage with the handle it came from, and even after that one is
  dropped a write through a stretched axis would land `n` times on one
  element. A kernel that writes takes a packed handle, so the licence
  reads: consumed, unique, and `is_packed()`.

## 9. What this does not decide

- **The kernels**: the rest of #9735. The first one exists, `__scale_f64`,
  the elementwise multiply over an `f64[]` that `std/array`'s `scale_f64`
  now is: scalar on all eight backends (`ATLAS-PLATFORM-PLAN.md` §3.4's
  steps 1 and 2, with the measurement), allocating its own result so no
  sized-array primitive was needed, and chosen over the dot product
  because a reduction may not reassociate (`ARRAY-ALGEBRA.md` §3) while a
  multiply has nothing to reassociate. `inner` and `outer` are recognized (§6) and
  lowered as the scalar loop; which of their sites a kernel replaces,
  and how a site's element functions are proved to be the arithmetic the
  kernel implements, is not decided here. §1 and §8 say what licenses an
  in-place elementwise op; nothing here takes it.
- **In-place through a handle.** The consuming-handle plus unique-storage
  rule in §1 is stated, not implemented; nothing in `std/ndarray` writes.
- **Static shapes.** §4.
- **The self-host.** `std/ndarray` compiles under the self-host where its
  element type does: a combinator handed a function over a 64-bit element
  is still refused on its wasm route (#9838), which this module does not
  yet do but phase 4 will.
