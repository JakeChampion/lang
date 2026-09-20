# 2026-09-20 — a mutable capture is a cell

The largest refusal leaf the corpus census reported after the OS floor
closed was `replacement of a capture`: eight lambdas that assign a captured
scalar, charged with 643 declarations across six programs, because a refused
`for_each` callback keeps the test that calls it on the AST lowering, and
through the prune cascade the whole test file with it. #9320 named the fix,
and this entry lands it.

## What was wrong

`capturebox` rewrote a mutated captured local into a one-element ARRAY and
its write into `$cell$x = $cell$x.with(0, v)`. The creator saw that write
only because nothing on either side took a count of the box, so the `.with`
in-place arm mutated the shared buffer. The typed boundary retains the
receiver to supply the update's unit, which lands the write in a copy nobody
reads, so it refused the write — and with it every caller of the lambda,
transitively.

Three more faults surfaced while measuring, none of them visible to the
relative pin:

- The typed lowering read a capture the CREATOR rebinds at its creation-time
  value whenever the closure ESCAPES: `s = s + "bb"` under a `read` handed to
  `apply` answered 103 for native's 104 and, over three rebinds, 3 for 15; an
  `i64` and a `boolean` rebound the same way answered 0 for 7. The creator
  was produced (its box "is its own at the write"), so its `.with` went into
  a copy the closure's environment never saw. The old spelling's refusal was
  only on the closure's side of the write.

- The box scan admitted only an `i32` or `boolean` the CLOSURE writes, on
  both lowerings. An `i64`, `f64`, `u8`, `u32` or `u64` the lambda assigned
  was captured by value and the writes were lost: `m = m + 3i64` inside a
  lambda left the creator's `m` untouched (37 for native's 51 on the probe).
- A `Cell[i64]` PARAMETER read inside i64 arithmetic bailed the AST lowering
  (`did not lower: call .set`): the parameter path set `is_cell` but not the
  element-width columns a `var c: Cell[i64]` local gets, so the `get` read as
  an i32.

## The fix

The capture box is the `Cell[T]` the language has:

    var x: T = init;      ->  var $cell$x: Cell[T] = cell_new(init);
    x                     ->  $cell$x.get()
    x = v                 ->  $cell$x.set(v);

and the lambda captures `Cell[T]`. A cell's box IS the one-element array box
the old spelling built (`ssarc.cell_read` indexes slot 0 with the array
ops; the AST lowering lowers `cell_new(v)` as `[v]`), so the representation
is unchanged and a produced closure and an AST-lowered creator still share
one box. What changed is the write: `set` is the element store on both
paths, releasing what the slot held, with no count that has to happen to be
one. The three consumers keyed on the `$cell$` NAME are gone with it —
irlower's borrowed-parameter exclusion and copy gate, and the boundary's
`foreign` marking of a cell parameter — since the type says what the name
used to. `capturebox.is_cell_name` had no reader left and is deleted.

The box scan boxes every scalar the closure writes, at its own type (E049
already makes such a capture a scalar).

On the AST lowering, a cell's element is classified as the array `T[]` it
is read as: `mark_cell_elem` goes through one type-driven marker
(`mark_array_slot_from_type`) for every element kind rather than the three
it used to spell out, a `Cell[T]` PARAMETER is routed through it when the
lowering state is built, and a `var c: Cell[T] = cell_new(init)` local is
lowered as `var c: T[] = [init]` with the cell marks on top, so the
initialiser's shape types the element as it always did for the array box.
Every `c.get()` on a local or parameter declared `Cell[T]` becomes `c[0]` at
the function's entry (`cell_reads_as_index`), before any analysis or type
predicate reads the body, since the predicates know an element behind an
index and only some of them behind a `get`. A name declared both as a cell
and as something else is left to the per-site desugar.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle:

| shape | native | before, typed / AST | after, typed / AST |
|---|---|---|---|
| `m = m + 1` in a lambda, i32 | 35 | refused / 35 | 35 / 35, produced whole |
| i64, f64, boolean written in a lambda | 51 | refused / **37** | 51 / 51 |
| u8, u64, f32, u32 written in a lambda | 143 | refused / **37** | 143 / 143 |
| `counter(c: Cell[i64])` with `c.set(c.get() + 1i64)` | 5 | refused / **bail** | 5 / 5 |
| a string rebound by the creator under an escaping closure | 104 | **103** / 104 | 104 / 104 |
| the same over three rebinds | 15 | **3** / 15 (96 B held) | 15 / 15 (96 B held) |
| i64 and boolean rebound by the creator under an escaping closure | 7 | **0** / 7 | 7 / 7 |
| array, string[], nested array, struct rebound under an escaping closure | as native | refused or **bail** | as native |
| `closure_capture_shared_cell` (conformance) | 0, same output | refused / 128 B held | 0 B held / 128 B held |
| string, array, struct, closure rebound by the creator | unchanged | 0 B / 0 B | 0 B / 0 B |

Every row is 0 bytes held on the typed lowering. The AST lowering's 128
bytes on the conformance case are its `to_string` results and the 96 bytes
on the rebound string are the superseded strings its cell write never
releases, both before and after this change: #9832's first half, the AST
lowering's release of what it no longer holds. The wasm leg answers the
same on both lowerings.

Corpus census (the conformance cases, coreutils, `examples/bench`,
`examples/cli`, `examples/tests` and the compiler; 863 that compile), root
refusals after following the prune cascades to the refusal they stand on:

| | before | after |
|---|---|---|
| programs produced whole | 810 of 863 | 815 of 863 |
| declarations produced | 81,366 of 86,922 | 82,586 of 86,922 |
| declarations charged to any root | 2,039 | 1,392 |
| `replacement of a capture`, declarations charged | 643 | 0 |

## What gates it

- `TestSelfHostSemanticProduction/a-capture-the-closure-writes-is-a-cell`:
  every scalar kind written in a lambda, a string the creator rebinds under
  an ESCAPING closure, and a `Cell[i64]` parameter in i64 arithmetic, pinned
  to native's answer on BOTH lowerings (`want` and `astAnswers`), since
  before this change they agreed with each other on the wrong one; `noLeak`
  on the sanitize leg. `capture-write` produces whole now rather than
  standing as the whole-module fallback it recorded.
- `TestSelfHostCaptureContractRewritesX86_64`: the three `cell keeps …`
  rows record `Cell[i64]`, `Cell[str]` and `Cell[(() => i64)]`.

## Traps

- **Both lowerings agreeing is not correctness.** The 37 on the wide probe
  was the same on the typed and the AST lowering, and the relative pin the
  earlier rows use would have passed it. A row that can be wrong on both
  sides needs native's answer, which `want` + `astAnswers` gives.
- **The census leaf is a cascade count, not a site count.** Eight sites cost
  643 declarations; the reducer has to follow `is reached by a direct call
  from X, which the AST lowering calls through a box` to X's own refusal, or
  the histogram is headed by the cascade's spelling and names nothing to
  build.
