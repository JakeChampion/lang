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

**What it recognizes is §7's closed list**: the eight operations that are
handed a function. `fern -array-report` lists every site under
`std/ndarray operations`, with the element functions the site was handed,
keyed on the `__method_ndarray__NdArray_` mangling that only
std/ndarray's receiver methods get. The three axis-parameterized verbs
also carry the argument that says which elements they walk —
`reduce_axis(axis 1)`, `scan_axis(axis 0)`, `map_rank(rank 2)` — read
only where it is written as a literal, and printed `axis ?` where it is
computed. A guessed axis would be worse than none: a reduction along the
last axis walks contiguous storage and one along any other axis strides,
so the two plan differently.

§2's structural operations are deliberately absent from that worklist.
They move no elements, so there is nothing for a kernel to replace — but
they decide what the operations BELOW them cost, which is the layout
analysis further down this section.

That list is the kernel half's worklist, and it answers the worklist's
own question rather than leaving the reader to go and look: each element
function is printed with what a kernel could do with it.

```
run:5:87  inner  over packed     mul [f64 mul], add [f64 add]  -> kernel-candidate
run:6:51  map    over strided    __closure_lambda_1 [element-fn-captures]  -> element-not-primitive
```

The first of those is `dot_f64` in disguise; the second is not, and says
why. `over` is the receiver's layout and `->` the site's verdict; both
are below, and the element functions in the brackets are what this part
of the section is about. The bar is `ATLAS-PLATFORM-PLAN.md` §3's: a
kernel is one IR op whose whole vector lifetime stays inside its own
emitted sequence, so a call to an element function is an op boundary and
nothing survives it. A kernel can only take an element function it can
INLINE, which means one primitive operation over the operands. `x * y`,
`x * 2.0` and `-x` all are — a unary is emitted as readily as a binary —
while a body with two operations or a call is not.

The name a primitive is printed under is the operation a kernel would
have to emit, not the source's spelling: `x < y` over `u64` prints
`u64 lt`, because the IR's `OpLtS` carries `Unsigned` and the
instruction is `lt_u`.

The refusals are a **closed set with stable tags**, for the reason
`FusionRefusal`'s are: they are the coverage checklist for widening the
kernels, and a checklist of free text cannot be tallied.
`FERN_ARRAY_REPORT=1` prints the tally, a row per reason even at zero.

| tag | what it means |
| --- | --- |
| `element-fn-unresolved` | the lowering did not park the function where the recogniser could name it |
| `element-fn-not-in-program` | named, but the program does not hold that function |
| `element-fn-captures` | a lambda closed over something, and a kernel has nowhere to put it |
| `element-fn-calls` | the body calls something, and a call is an op boundary |
| `element-fn-not-one-op` | the body is more than one operation, or applies none |
| `element-fn-not-arithmetic` | the body's one operation is not arithmetic a kernel emits inline. Two absences are deliberate: INTEGER division and remainder, because both trap on a zero divisor and a kernel that hoisted one would move the trap (float division stays, which does not trap); and conversions, because they change the element type, which makes the stage a different shape rather than a kernel over this one |

Each line then carries the SITE's verdict, which is the question a
planner actually asks: every element function primitive is necessary and
not sufficient, because an axis verb also has to say which elements it
walks.

```
algebra:11:60  reduce_axis(axis 1)  over unknown    add [i64 add]  -> kernel-candidate
algebra:13:48  map_rank(rank 1)     over unknown    sum_cell [element-fn-calls]  -> element-not-primitive
algebra:14:60  reduce_axis(axis ?)  over unknown    add [i64 add]  -> axis-not-literal
```

The third of those is the one a per-element reading cannot see: its
element function is primitive, and only the site-level question declines
it. `axis-not-literal` is its own row for the reason the axis is read at
all — a reduction along the last axis walks contiguous storage and one
along any other axis strides, so a kernel cannot be selected without
knowing which. The axis is half of that question: the last axis of a
STRIDED handle is not contiguous either, and the layout below is the
other half. Each site's line carries both.

The site verdicts are a closed set too, and their tally sits beside the
element one under `FERN_ARRAY_REPORT=1`.

| tag | what it means |
| --- | --- |
| `kernel-candidate` | nothing this pass can see would make a planner decline the site |
| `element-not-primitive` | an element function is not one a kernel can inline, per the table above |
| `axis-not-literal` | the axis is not a literal, so which elements the kernel would walk is not known here |

### What a handle's layout is, and where it is proved

**Normative.** §2's last three rows — `reshape`, `packed()`, `to_flat()`
— branch at RUN TIME on `is_row_major()` / `is_packed()`. So two
textually identical calls cost different amounts:

```
var a = nd.from_flat(xs, s);   var f1 = a.to_flat();        // free
var t = a.transpose();         var f2 = t.to_flat();        // O(n)
```

Both lower to the same op with the same callee. That is the avoidable
O(n) copy #9734 says concise array code must not conceal, sitting inside
the IR unremarked — and §8's licence for an in-place elementwise kernel
needs the same fact from the other direction, since it reads "consumed,
unique, and `is_packed()`" and a planner can take it only where the third
conjunct is decided before the program runs.

Both predicates are decided by the metadata, and the metadata is decided
by the chain of operations that built the handle — every one of which §2
names. So **the layout is a property the IR carries**, derived from
provenance: `internal/ir/ndarray_layout.go`, printed per site by
`fern -array-report`.

| layout | what it asserts |
| --- | --- |
| `packed` | `is_packed()` is provably true, so all three of §2's branching rows take their free arm |
| `row-major` | `is_row_major()` is provably true and `is_packed()` is not proven, so `reshape` is metadata and `to_flat()` may copy |
| `strided` | the provenance WAS followed, to a §2 metadata operation that preserves neither predicate |
| `unknown` | the provenance was not followed: a parameter, a struct field, a call whose callee the summary below does not settle |

`strided` and `unknown` both claim nothing, and are separate rows
because they fail for different reasons — which is what makes the tally
a coverage checklist rather than a count.

The transfer functions are §2's table and §7's, read forwards, and each
is the operation's own code:

- `from_flat` aborts unless `count_of(shape) == data.len()`, then takes
  the row-major strides at offset 0 — all three conjuncts, so **packed**.
- the metadata operations (`transpose`, `permute`, `reverse`, `slice`,
  `select`, `broadcast_to`) keep `data` and perturb the strides, the
  offset or both, so **strided**. None preserves a predicate in a way
  that survives an unknown rank, and §4 makes the rank a run-time
  property.
- `reshape` is **row-major** whichever branch it takes: the metadata
  branch writes `row_major(shape)` outright and the copying branch is a
  `from_flat`. It is **packed** as well from a packed receiver, whose
  offset is 0 and whose storage holds exactly the count the new shape
  must have.
- `packed()` is **packed** from anything: it returns the receiver when it
  is packed and a `from_flat` copy otherwise.
- every operation of §7 that returns a handle returns `from_flat` of a
  buffer it just filled, so **packed**. `fold_all` returns a scalar and
  `to_flat` a `T[]`, so neither produces a layout.

Slots are **flow-insensitive**: a slot's layout is the meet of every
layout stored into it anywhere in the function, and a parameter claims
nothing. A slot holding a packed handle on one branch and a transposed
one on the other therefore reads `strided` at both. That costs precision
and needs no reasoning about which store reaches which load, so the
answer is correct whatever the control flow does. Two stores are not
counted: a constant, which is the null the lowering writes into a slot it
has moved the handle out of and which no handle ever is; and anything
reached after the operand stack stopped being tracked, which claims
`unknown`.

A CALL is followed when the callee's own returns settle it. A function
whose every return is a handle of one layout is a producer of that
layout, exactly as `packed()` is, so a program that builds its handles in
a helper reads the same as one that inlines them. The summary is the
**meet** over the function's returns, so a helper returning a packed
handle on one arm and a transposed one on the other claims `strided`, not
the arm a given call site took. A recursive function has its cycle **cut**
rather than followed: the arm reaching it reads `unknown`, which is what
bounds the summary. The function itself is not thereby nothing — it
still settles at the meet over its arms, so one whose base arm is a
`from_flat` and whose recursive arm is a `reshape` reads `row-major`. A
function whose declared result is not a handle reads `unknown`, and so
does one the program does not define.

What a return summary cannot reach is the other direction. A receiver
that is its own function's **parameter** claims nothing, because layouts
would have to travel from callers into callees, and they do not. Over
`examples/tests/ndarray_test.fern` that is the whole remainder: 14 of 43
reported rows read `unknown` before the summary and 1 after, and the one
is a parameter.

Each storage-sensitive site then carries a verdict, closed and tagged
like the kernel sets above, tallied under `FERN_ARRAY_REPORT=1`:

| tag | what it means |
| --- | --- |
| `metadata` | the receiver's layout proves the free branch, so no element moves |
| `not-proven-packed` | `to_flat()` or `packed()` on a receiver not proven packed, so the call may copy |
| `not-proven-row-major` | `reshape()` on a receiver not proven row-major, so the call may copy |

```
main:5:10  to_flat  over packed     -> metadata
main:5:27  to_flat  over strided    -> not-proven-packed
```

**This changes nothing.** `metadata` says the copy is provably absent,
not that anything was removed; the call still runs `std/ndarray`'s own
branch and takes its free arm at run time, exactly as before. What is new
is that the answer exists before the program does, which is what §8's
in-place licence and #9735's kernels need and could not ask for.

Recognition and both verdicts change nothing. **A verdict of `primitive`
says the element function is not what stands in the way, and
`kernel-candidate` says nothing this pass can see would make a planner
decline the site — neither says that the site lowers to a kernel** — no
kernel exists for any verb yet.
Every site still runs the scalar loop, nothing fuses, nothing donates,
and `internal/ir/ndarray_shapes_test.go` pins that the recogniser
changes no op.

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
| `map_rank(k, f)` | `frame ++ f's result shape`, the frame being the leading `rank - k` axes; the empty handle of shape `frame` when there are no cells | cells in increasing index order, each cell's result read in reading order | one packed buffer of the result count |

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
  now is: vectorised on all eight backends, and reached by
  `xs.map((x: f64): f64 => x * k)` without the wrapper being written, for a
  literal k or a captured one (`ATLAS-PLATFORM-PLAN.md` §3.4's four steps,
  with the measurements), allocating its own result so no
  sized-array primitive was needed, and chosen over the dot product
  because a reduction may not reassociate (`ARRAY-ALGEBRA.md` §3) while a
  multiply has nothing to reassociate. `inner` and `outer` are recognized
  (§6) and lowered as the scalar loop; which of their sites a kernel
  replaces, and how a site's element functions are proved to be the
  arithmetic the kernel implements, is not decided here. §1 and §8 say
  what licenses an in-place elementwise op; nothing here takes it.
- **In-place through a handle.** The consuming-handle plus unique-storage
  rule in §1 is stated, not implemented; nothing in `std/ndarray` writes.
  §6's layout analysis decides the `is_packed()` conjunct §8 adds to it,
  and decides nothing else: "consumed" and "unique" are the reuse passes'
  and the run-time guard's, not this one's.
- **Acting on a layout.** A `metadata` verdict licenses a rewrite —
  dropping the run-time branch, or selecting a contiguous kernel over a
  strided walk — and nothing takes it. The analysis reports; §6 says so.
- **Static shapes.** §4.
- **The self-host.** `std/ndarray` compiles under the self-host where its
  element type does: a combinator handed a function over a 64-bit element
  is still refused on its wasm route (#9838), which this module does not
  yet do but phase 4 will.
