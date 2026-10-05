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
  element count — `data` IS the reading order. The extent-1 skip above
  carries into it, so a `broadcast_to` that only PREPENDS extent-1 axes
  leaves a packed handle packed; one that stretches an axis gives that axis
  a stride of 0 at extent greater than 1, and does not. The same skip means
  a packed handle may carry a NON-CANONICAL stride on an extent-1 axis —
  `reverse` of such an axis negates its stride and moves nothing, so the
  handle stays packed with a stride the row-major ones do not contain. No
  element read steps along that axis, so values are unaffected; anything
  deriving a view's strides must take them from the handle rather than
  recompute them from the shape.

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
to; `std/ndarray`'s operations are ordinary Fern whose declaration origin
the primary compiler tracks from loading through generic instantiation.
The `ndarray__` prefix identifies candidates, but only verified stdlib
declarations supply layout guarantees and kernel contracts. A user module
named `ndarray.fern` retains its own behavior, even when its types and method
signatures match the stdlib. The struct's layout is
therefore the representation: four fields the IR reads as any struct's,
which is what lets a kernel take `data`, `shape`, `strides` and `offset`
without a second description of them.

**Primary compiler.** `FERN_ARRAY_REPORT=1` on a compile prints the actual
ndarray kernel planner's decisions, alongside fusion and map storage. It
lists all eight operations from §7, including calls that stay scalar. Each
site includes its receiver layout, the second operand's layout for binary
operations, resolved element-function names, and a literal axis or rank where
available. A computed axis is `?`, never guessed.

The current kernels are packed `f64` map with a capture-free literal scale,
outer product with a capture-free binary multiply, and inner product with
capture-free binary multiply and add.
`scale-kernel` is recorded by the rewrite itself with
`storage=deferred-to-ownership`. The final `scale storage` report states whether
the scale kernel uses a fresh buffer or guarded reuse. `outer-mul-kernel`
and `inner-mul-add-kernel`
always report `storage=fresh-kernel-buffer`. Other operations remain ordinary
scalar combinators. Their closed refusal tags are:

| Tag | Planner gate |
| --- | --- |
| `disabled` | An otherwise eligible site was disabled by `FERN_NO_SCALE_KERNEL=1` (map) or `FERN_NO_PRODUCT_KERNEL=1` (outer or inner). |
| `layout-unknown` | Packed layout was not proved. |
| `layout-strided` | A metadata transform prevents a packed proof. |
| `layout-row-major` | Row-major order is known, but packed storage is not. |
| `element-fn-unresolved` | The function is not a locally resolved closure body. |
| `element-fn-captures` | The closure carries captures. |
| `element-not-literal-scale` | The body is not exactly element times an `f64` literal. |
| `element-not-binary-multiply` | The body is not exactly the left element times the right element, in that order. |
| `element-not-binary-add` | The body is not exactly accumulator plus product, in that order. |
| `constructor-unavailable` | A required constructor, shape helper or checked kernel adapter is unavailable. |
| `builtin-shadowed` | A user declaration shadows the selected kernel builtin. |
| `unsupported-operation` | No kernel currently replaces this algebra operation. |
| `unsupported-element-type` | This is not a supported `f64` map, outer or inner instance. |

The histogram includes zero counts. Reporting on and off must emit identical
code; tests compare assembly and verify kernel instructions at reported sites.

The final ownership plan may donate an owned data buffer whose unit dies at
the scale call. Guarded record-field takes establish ownership for consumed
handles; borrowed fields and still-live values cannot donate. A runtime
uniqueness test selects reuse or a fresh result, preserving shared handles and
separate aliases of their data. The shared arm scales directly into the new
buffer, without first copying the input. Each arm keeps its vector lifetime
inside the kernel.
The output still uses `from_flat`, preserving canonical result metadata.
Buffer reuse does not promise that the whole operation allocates nothing.

`scale storage` uses these closed reasons: `guarded-reuse`, `reuse-disabled`,
`receiver-borrowed`, `receiver-still-live`, `receiver-supplied`,
`borrow-linked-result`, `builtin-shadowed`, and `shape-unsupported`.
`FERN_SELFHOST_NO_REUSE=1` disables donation while retaining the fresh-buffer
SIMD kernel; `FERN_NO_SCALE_KERNEL=1` disables the ndarray scale rewrite.
`FERN_NO_PRODUCT_KERNEL=1` disables the outer and inner rewrites. Intrinsic refusal
reasons survive both switches; only otherwise eligible sites report `disabled`.

Layout facts come from the same interprocedural analysis that gates rewrites.
Outer and inner sites must pass both packed-layout proofs and the exact
element-body proofs below before the report identifies them as kernels.

**Retained Go analysis.** The older `-array-report` interface described below
is labelled as Go analysis and does not describe primary compiler output.
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
| `element-fn-not-arithmetic` | The body's one operation is outside the retained classifier's supported arithmetic set. Integer division and remainder are excluded, although Fern defines them as total even for zero divisors (`ARRAY-ALGEBRA.md` §2). Conversions are excluded because they change the element type. |

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
let a = nd.from_flat(xs, s);   let f1 = a.to_flat();        // free
let t = a.transpose();         let f2 = t.to_flat();        // O(n)
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

The retained Go report only propagates return summaries. Its parameter
receivers remain unknown: over `examples/tests/ndarray_test.fern`, 14 of 43
reported rows read `unknown` before those summaries and 1 after, a parameter.
The primary compiler's typed analysis also propagates arguments from callers
into callees, as described below. The historical Go report's counts do not
measure that analysis.

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
decline the site — neither says that the site lowers to a kernel.**
`internal/ir/ndarray_shapes_test.go` pins that the recogniser changes no
op, and the layout analysis below changes none either.

One verb now has a kernel. `map` by a scalar `f64` factor over a handle
the layout analysis proves **packed** becomes

```
from_flat(__fern_scale_f64(a.data, k), a.shape)
```

— `internal/ir/ndarray_scale.go`, the std/array kernel of #9735 reached
through a shape. Packed is what licenses it: `data` IS the reading order,
so the kernel reads the elements the scalar walk would have visited. Over
a strided handle the same rewrite computes DIFFERENT numbers rather than
the same ones faster, which is why the gate is the proof and not the
verb. Everything else still runs the scalar loop, and nothing fuses or
donates on this side.

The primary Fern compiler reaches the same kernel through
`examples/self_host/semndarray.fern`, on typed semantic values before ownership
planning. `semndlayout.fern` carries the layout lattice through constructors,
structural operations, copies, joins, helper returns and direct-call arguments.
Its return summary meets every return, and each parameter meets every caller's
argument. A helper called with both packed and strided handles therefore
cannot take a packed-only kernel, regardless of caller order.
The element function must be a capture-free closure whose body is exactly
the element multiplied by an `f64` literal. It constructs the result through
`from_flat`, which preserves the shape and produces canonical row-major
strides. Ownership planning retains the shared shape and releases temporary
storage through its ordinary rules.

The analysis distinguishes an unseen fact from an unknown layout. Facts only
lose precision as the fixed point propagates; recursive calls and loop phis
participate without a guessed iteration limit. A packed seed can prove a
packed recursive result. A mixed recursive edge loses that proof. Remaining
unseen facts become unknown and propagate again before any rewrite uses them.

Explicit exports, methods and closure entry points keep unknown parameters,
because a direct-call census cannot account for all their callers. Source
visibility is different: flattening has merged the modules and marked their
declarations public, so `is_pub` does not describe external entry points.
Hand-written record literals, fields, cells and unresolved calls also claim
nothing. No nominal type alone proves storage layout.

The loader supplies stdlib provenance separately from function names. The
planner checks it for the operation, constructor and metadata helpers, and
the layout analysis checks it before applying a stdlib return guarantee.
Relative user imports and flat-name or manifest fallbacks do not gain that
provenance from their spelling. Execution regressions cover a custom module
whose `map` and `outer` reverse the left input, both import orders alongside
the real stdlib, and a missing stdlib that resolves through a local fallback.

Captured factors and unresolved element functions still use scalar maps.
These are coverage limits, not claims that those cases cannot be optimized.
`FERN_NO_SCALE_KERNEL=1` disables this rewrite. The primary gate
checks emitted instructions as well as values: a packed map emits the SIMD
scale, while mixed callers, indirect calls, exports and unproven recursive
returns retain their map calls. Reporting parity and broader element-function
proofs remain part of #9727.

Measured on arm64-darwin on 2026-10-05 with
`examples/array_pipeline/ndarray_scale.fern`: the same candidate compiler,
kernel enabled versus `FERN_NO_SCALE_KERNEL=1`, 1,000 elements, 200 rounds
per process, nine alternating runs of each build. Median time per map was
253 ns enabled and 2,235 ns disabled (8.83x). Every checksum agreed. The
enabled build used 6 cold allocator calls and 8,288 fresh bytes; the disabled
build used 15 and 10,392. Over 200 rounds, the enabled build used 1,400
allocator calls and 8,344 fresh bytes; the disabled build used 3,200 and
10,392. Counters include metadata and result checks;
timing covers only the map. The first result stays live across the run, and
every later result is checked after its timer. This is a native measurement
for this input size, not a claim about other targets or shapes.

The consuming-input probe is `examples/array_pipeline/ndarray_owned_scale.fern`,
run with `1000 200 0` for unique inputs and `1000 200 1` for live aliases.
On arm64-darwin on 2026-10-05, nine alternating processes per build and mode
reported these counters, including metadata and checks:

| Input | Before donation: calls / fresh bytes | With donation: calls / fresh bytes |
| --- | --- | --- |
| Unique | 1,000 / 16,440 | 800 / 56 |
| Shared | 1,400 / 16,560 | 1,400 / 16,560 |

Median times per map were 252 ns before and 235 ns after for unique input,
240 ns before and 235 ns after for shared input. The ranges overlap, so this
establishes an allocation reduction, not a timing improvement. Every element
and live alias was checked outside the timer. An initial shared implementation
copied and then scaled, regressing to 538 ns; the direct fresh-output arm
removed that extra traversal before publication. Target tests repeat both
counters and alias checks on x86-64, ARM64 and WASM with reuse on and off.

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
  that reassociates it is wrong, not fast. A packed handle takes a
  DIRECT-INDEX walk rather than the odometer — element `i` of the reading
  order is `data[i]` when `data` is the reading order — and owes the same
  order by the same rule. `map`, `fold_all`, `zip_with`, `reduce_axis` and
  `scan_axis` all take it. `map_rank` is the one verb that does not walk
  elements at all — it peels CELLS — but it has the same shape of fast
  path: a packed handle's cell at frame index `i` is that handle with its
  offset moved to `i * csize`, the same shape and strides every time, so
  it replaces a chain of `select` calls that each allocated a fresh pair.
  The axis
  folds walk in reading order like the rest (see the next rule), so what
  they need on top is the LANE: with `inner` the product of the extents
  after `axis` and `outer` the product before it, three counters enumerate
  the reading order and the lane is `h * inner + l`, which costs the same
  whichever axis is named. `zip_with` asks the predicate of BOTH operands and
  asks it AFTER broadcasting, which needs no separation of the broadcast
  case: §2's rule means an operand whose reading order a broadcast
  changed already fails it. The e2e gate holds both arms with an
  order-sensitive fold, over a transpose, over a packed handle, and over
  the prepending broadcast of §2 that stays packed; a commutative `add`
  cannot tell them apart, which is why the fold that
  guards this multiplies by ten, and why the packed `zip_with` case pins
  every position under an element function that is not symmetric. The
  packed axis folds are pinned on two axes, not one: along the last axis
  a lane is a contiguous run, so only a fold along an earlier axis — where
  consecutive elements belong to DIFFERENT lanes — can catch the lane
  arithmetic being wrong.
- **`reduce_axis` is lane-sized.** It walks the input once in reading
  order and keeps one accumulator per lane (the row-major position in the
  shape with `axis` removed), so its allocation is the result, never a
  copy of the input. `internal/e2e/ndarray_test.go` bounds it under the
  element buffer where `map` is bounded above it.
- **A shape mismatch aborts.** `zip_with` over two shapes that do not
  broadcast (§8) is a derived shape that is wrong (`ARRAY-ALGEBRA.md`
  §4), and takes the same status an out-of-range index does (§5).

`outer` also walks data directly when both operands are packed. Its nested
loop visits every right-hand element for each left-hand element and retains
the callback, including captures and effects. It checks the joined shape for
overflow before either path. Strided inputs retain the broadcast-view walk.
Both paths produce canonical result strides, including when an input has a
noncanonical stride on an extent-one axis.

On arm64-darwin on 2026-10-05, `examples/array_pipeline/ndarray_outer.fern`
with two 32-element vectors and 200 rounds, nine alternating processes per
build, measured median times of 10,036 ns before and 2,465 ns after this
packed scalar path. The ranges were 9,682-10,666 ns and 2,360-2,548 ns.
Allocator calls fell from 6,200 to 3,600 and fresh bytes from 20,248 to
20,016. Every result and both inputs were checked outside the timer.
This is the scalar baseline for subsequent product kernels.

The primary compiler also provides `std/array.outer_mul_f64(a, b)`, backed
by `__outer_mul_f64`. It borrows two flat `f64[]` inputs and allocates a fresh
array in left-index-first order: element `i * b.len() + j` is `a[i] * b[j]`.
Empty inputs produce an empty array. A length product above signed i32
aborts before allocation (status 134 on native targets, a bounds trap on
WebAssembly). SSE2, NEON and WebAssembly SIMD each multiply two right-hand
elements per iteration, with an ordered scalar tail. Operand order is
preserved; no vector value survives a call. Non-NaN results must match the
scalar reference bit-for-bit. Arithmetic NaN payloads follow FS-04 and may
differ between backends; both inputs must retain their exact bits.

An explicit-kernel probe in the same benchmark, on Apple M3 Pro,
arm64-darwin, 2026-10-05, compared 32-by-32 results over 200 rounds in nine
alternating processes. The packed scalar stdlib path measured a median
2,540 ns per outer product (range 2,493-3,064); the kernel probe measured
297 ns (284-323). Allocator calls were 3,600 versus 2,000, and fresh bytes
20,016 versus 10,472. Both modes checked all results and both source arrays
outside the timer. Only the round count changed from the two-round pilot.
This measures explicit kernel use on those inputs.

The primary compiler also selects that kernel for `a.outer(b, (x: f64,
y: f64): f64 => x * y)` when both layouts are proved packed. Its typed
element-body proof requires a resolved, capture-free closure that returns
exactly one multiply of its left and right element parameters, in that
order. Calls, branches, conversions, extra arithmetic and reversed operands
keep the scalar path. A shadowed builtin also prevents the rewrite.
Joined-shape validation runs before the kernel allocation: prefix overflow
still fails even if a later extent is zero. `from_flat` builds canonical
result metadata, including rank-zero and empty products. The kernel borrows
both inputs, so aliased operands remain unchanged. The differential checks
non-NaN result bits, NaN classification and exact input bits. It and the
ownership census run with the rewrite enabled and disabled on x86-64, arm64
and WebAssembly.

For the final ndarray rewrite, the same machine, inputs and nine alternating
processes measured 279 ns per outer product (259-295) with rewriting enabled
versus 2,554 ns (2,489-2,715) with `FERN_NO_PRODUCT_KERNEL=1`. Both builds ran
the ordinary stdlib-call mode, with every output and both inputs checked
outside the timer. Over 200 rounds, allocator calls were 1,600 versus 3,600
and fresh bytes 10,472 versus 20,016. The two-round pilot changed only its
round count for this measurement.

General `map` and `zip_with` allocate their result even when the receiver is
consumed and unique. The primary compiler can donate the packed literal-scale
map's data buffer under the ownership and uniqueness conditions in §6.

`inner` uses direct row/contracted-index/column addressing when both operands
are packed. Output positions retain reading order, and each output calls
`mul` then `add` for every increasing contracted index. Captured state and
noncommutative callbacks therefore observe the same sequence as the strided
walk. Geometry comes from shapes, so a zero contracted extent produces the
initial accumulator at every output. Output-shape validation precedes the
packed branch; an empty output returns before deriving unused sub-products.

On Apple M3 Pro, arm64-darwin, 2026-10-05, the ordinary-call mode of
`examples/array_pipeline/ndarray_inner.fern` compared the original and packed
stdlib builds at 32-by-32-by-32 over 200 rounds in nine alternating processes.
The original median was 153,062 ns (151,526-161,258), and the packed median
141,225 ns (140,203-145,197). Both checked every output bit and source element
outside timing, with an initial accumulator of 0.25. Only rounds changed
from the two-round pilot. Allocator calls rose from 3,800 to 4,000 because
the two packed predicates build stride arrays while the packed path removes
one index array; fresh bytes stayed at 10,472.

`std/array.inner_mul_add_f64(a, b, rows, extent, columns, init)` provides an
explicit packed contraction kernel. It validates dimensions, input lengths
and the signed-i32 output limit before allocating a fresh result. Each output
starts at `init` and performs a separate multiply then add for every increasing
contracted index. A zero extent copies `init` exactly, including signed zero
and NaN bits. Inputs remain unchanged. SSE2, NEON and WASM SIMD process two
independent output columns together, with a scalar odd-column tail. There is
no horizontal reduction, reassociation or fused multiply-add.

On the same machine and date, the benchmark's ordinary packed scalar mode
and explicit kernel mode measured medians of 141,920 ns (140,200-152,193) and
10,748 ns (10,468-10,897), respectively. Inputs were 32-by-32-by-32 with an
initial accumulator of 0.25; nine alternating processes ran 200 rounds after
a two-round pilot. Every output bit and source element was checked outside
timing. Allocator calls were 4,000 versus 2,000 and fresh bytes 10,472 versus
10,456. These measurements compare explicit kernel use with the packed scalar
stdlib call, before automatic selection.

Adding the explicit mode increased this benchmark's native text by 1,064
bytes, with unchanged data size. Compiling the previous benchmark source
with the new compiler produced an identical executable: unused kernel support
adds no code to that program.

The primary typed planner selects the inner kernel when both operands are
proved packed and the resolved callbacks are exactly `x * y` and `acc + value`,
with `f64` parameters and results. Captures, calls, extra arithmetic and reversed
operands retain the scalar path. A private checked adapter preserves rank and
contraction validation, output-shape prefix overflow, arbitrary initial values
and the early empty-output return. Geometry comes from shapes even when the
contracted extent is zero. The result has canonical packed metadata, including
when an input's extent-one strides are noncanonical. The rewrite respects
builtin shadowing and `FERN_NO_PRODUCT_KERNEL=1`.

With automatic selection, the same ordinary `inner` source measured a median
of 6,311 ns (6,081-8,294) enabled and 133,321 ns (128,254-181,689) disabled on
the Apple M3 Pro on 2026-10-05. Nine alternating processes ran 200 rounds at
32-by-32-by-32 after a two-round pilot, checking every output bit and input
element outside timing. Allocator calls were 2,000 versus 4,000; fresh bytes
were 10,472 in both builds. The enabled executable used 1,112 more text bytes
for the checked adapter and kernel, with unchanged data size.

After integrating the later fusion passes, ready-successor block layout and
typed-contract retirement, a fresh compiler repeated both ordinary-call
benchmarks on the same Apple M3 Pro. A two-round pilot preceded 200 rounds,
with nine alternating processes and only `FERN_NO_PRODUCT_KERNEL` changed
between builds. The outer inputs were 32-by-32 and the inner geometry was
32-by-32-by-32, with the same 0.25 initial accumulator:

| Operation | Kernel enabled, ns/operation | Kernel disabled, ns/operation | Allocator calls, enabled/disabled |
| --- | ---: | ---: | ---: |
| outer | 238 (228-246) | 2,241 (2,195-2,386) | 1,600 / 3,600 |
| inner | 5,982 (5,717-6,350) | 128,045 (125,963-136,920) | 2,000 / 4,000 |

Times are medians with observed ranges; counts cover all 200 operations.
Every output and both inputs were verified outside timing. Fresh bytes stayed
at 10,472/20,016 for outer and 10,472/10,472 for inner, enabled/disabled.
Both kernels retain their measured advantage with the combined compiler.

Source tree shaking retains the adapter until typed selection. When no site
uses it, final lowering removes the adapter and its otherwise unused ordinary
ndarray callees while preserving exports, shared callees and function-address
references. This removes 1,104 unused text bytes from the disabled probe and
restores its previous text size. Separate-module tests cover both enabled and
disabled linking on x86-64 and arm64.

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

### Product measurements after compiler integration

After main's single-use inliner, construction splitting, SSA join homes and
closure-lifting changes were integrated through `6c7dd98fa`, the native
compiler reproduced itself byte-for-byte. Product benchmark assembly changed,
so both ordinary-call kernels were remeasured on Apple M3 Pro arm64-darwin.
A two-round pilot preceded nine alternating 200-round processes at 32 by 32
for outer and 32 by 32 by 32 for inner, changing only the round argument.
Each executable verifies its result against the scalar reference.

| Operation | Kernel, ns/call | Scalar path, ns/call | Allocator calls, kernel/scalar | Fresh bytes, kernel/scalar |
| --- | ---: | ---: | ---: | ---: |
| outer | 258 (243-274) | 2,439 (2,362-2,527) | 1,600 / 3,600 | 10,472 / 20,016 |
| inner | 6,210 (6,046-6,873) | 140,977 (140,407-146,292) | 2,000 / 4,000 | 10,472 / 10,472 |

Times are medians and observed ranges. Counters cover all 200 calls. Both
kernels remain faster than their scalar paths; no baseline was increased.

## 9. What this does not decide

- **The kernels**: the rest of #9735. The first one exists, `__scale_f64`,
  the elementwise multiply over an `f64[]` that `std/array`'s `scale_f64`
  now is: vectorised on all eight backends, and reached by
  `xs.map((x: f64): f64 => x * k)` without the wrapper being written, for a
  literal k or a captured one (`ATLAS-PLATFORM-PLAN.md` §3.4's four steps,
  with the measurements). Its fresh-output form allocates the result, so no
  sized-array primitive was needed. It was chosen over the dot product
  because a reduction may not reassociate (`ARRAY-ALGEBRA.md` §3) while a
  multiply has nothing to reassociate. The same kernel now reaches the
  ndarray `map` of that shape over a packed receiver (§6). The primary
  compiler also selects binary-multiply `outer` sites with two packed
  operands, using the exact body proof above. It also selects the inner kernel
  in §7 after proving both callbacks, preserving scalar reduction order.
  The primary compiler's literal-scale kernel can donate its buffer through
  the ownership plan and runtime guard described in §6.
- **Other in-place operations through a handle.** Donation currently covers
  the packed literal-scale kernel. General `map` and `zip_with` still allocate
  their result buffers. Layout analysis proves packed order; the ownership
  plan and runtime guard establish the right to modify storage.
- **Other layout rewrites.** The primary compiler selects packed scale and
  outer-multiply and inner-multiply-add kernels. Eliminating metadata
  operations' runtime branches is separate work.
- **Static shapes.** §4.
- **The self-host.** The primary compiler's ndarray kernel, storage and
  algebra tests cover x86-64, ARM64 and WASM, including `f64` closures.
