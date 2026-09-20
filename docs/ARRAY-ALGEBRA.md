# The array algebra

Status: policy doc, normative where marked. Indexed by
`spec/semantics.md` (`AA-*`).

The seven semantics questions of #9729, answered before a fusion pass
exists rather than discovered by a test failing after one lands. Each
answer changes what such a pass is allowed to do, so each is settled
here and cited there.

`docs/ITERATOR-FUSION-CONTRACT.md` is **upstream of this document, not
parallel to it**. Where it already answers a question — the operator
algebra, the compositional guarantee, the parity bar, visible failure —
this cites it rather than restating or weakening it. Where the eager
combinators need a different answer from lazy chains, that is said and
why. `docs/ARRAY-PIPELINE-BASELINE-2026-09.md` is the measurement these
answers are aimed at.

## What is already decided, and what this had to decide

Three of the seven had partial answers elsewhere in the tree. Two turned
out to have none at all, and that is the more useful finding: **a rule
that reads as "fusion must not change the existing guarantee" is
unenforceable where there is no existing guarantee to not change.**

- **Evaluation order is unspecified.** `spec/semantics.md` says so
  outright: `AL-06` "is the only evaluation-order claim, and it covers
  one construct: where every other expression evaluates its parts is
  still whatever the two compilers happen to agree on." So #9729's
  question 2 cannot be answered by preserving something. §2 below
  specifies the ordering array pipelines need, because nothing else
  does.
- **There is no purity notion in the language surface.**
  `docs/EFFECT-ROWS-BRIEF.md` records the outcome plainly: "the analysis
  ships; the surface syntax does not … No new surface syntax, no new
  diagnostic, no commitment to an effect system." So question 1 cannot
  be answered by "restrict fusion to pure functions" either — there is
  no predicate to ask. §1 says what to use instead.

## 1. The purity boundary

**Normative.** A combinator call is *fusible* when every element
function it is given is **statically resolved** and reaches **no
capability-tagged builtin** under `internal/effects`' call graph. A call
whose element function is not statically resolved — a value from a
parameter, a field, an array element — is not fusible, whatever it
turns out to hold at run time.

The reachability is the one the compiler already computes. `fern
-effects` reports per-function effect sets over
`internal/effects`' shared call graph, and `internal/caps` was already
rebuilt on that graph rather than walking the call graph a second time.
A fusion pass is the third consumer of the same analysis, not a new one.

Two consequences worth stating, because both are easy to get wrong:

- **`ViaIndirect` is not empty and must be treated as effectful.** The
  effects reached only through a call that could not be resolved to a
  name are, as the brief puts it, "the price of having no effect row on
  function TYPES". A pipeline whose element function is an opaque value
  therefore does not fuse. That is the same boundary
  `ARRAY-PIPELINE-BASELINE-2026-09.md` measured as costing 26-39% of the
  gap on native and 51-73% on self-host output — so the fusible set and
  the specialisable set are the same set, which is convenient rather
  than coincidental.
- **"Pure" is vocabulary-relative.** The brief is explicit: a function
  reaching nothing under the authority vocabulary reaches `log` under
  the host one, and "a function 'pure' under one is not pure under the
  other." Fusion uses `internal/caps`' vocabulary — `env fs net random
  subprocess time` — because that is the one with an enforcement story
  behind it (E070). Allocation and abort are **not** effects in any of
  these vocabularies, which §2 depends on.

What this deliberately does not do is introduce an effect annotation.
The brief killed `uses [...]` on purpose; a fusion pass is not the
reason to revive it.

## 2. Evaluation and error ordering

There is no general evaluation-order guarantee to preserve, so this
specifies one for the pipeline shapes, narrowly.

**Normative.** For a fused pipeline over an array:

1. Element functions are applied in **increasing index order**.
2. Fusion **preserves the multiset of element-function applications**.
   If the unfused pipeline applies `f` to every element, the fused one
   does too.
3. A pipeline shape whose fusion would **reduce** the number of
   applications — a short-circuiting or bounded consumer placed after a
   `map` — is fused only when the elided applications are
   **unobservable**: the element function is fusible by §1 and cannot
   abort. Otherwise the shape is not fused, and says so (§7).

Rule 3 is the whole of the difference from a lazy chain, and it is why
this document exists beside `ITERATOR-FUSION-CONTRACT.md` rather than
inside it. A lazy `xs.map(f).take(3)` applies `f` three times **by
construction**; that is the semantics the user asked for. The eager
`xs.map(f).take(3)` applies `f` to every element and then discards, and
a fusion pass that quietly turns the second into the first has changed
what the program does.

**What "cannot abort" means is narrow, and that is what makes rule 3
usable.** Fern's trap surface is small and written down:

- Integer division is **total** — `x / 0 == 0` and `x % 0 == x`
  (`IS-04`, `IS-05`), and `MIN / -1 == MIN` (`IS-06`). There is no
  arithmetic trap to preserve.
- Float arithmetic does not trap; the edges are under-specified values,
  not faults (`docs/FLOAT-SEMANTICS.md`).
- What does abort is **indexing out of range** (`AB-01`..`AB-03`), slice
  construction outside its range (`AB-04`), `slice_unchecked`
  (`ST-04`), and an allocation size that does not fit.

So an element function that indexes nothing and slices nothing cannot
abort, and that is a property a pass can check syntactically over the
already-resolved body. A function that does index is not thereby
unfusible — it is only unfusible **in a position where fusion would
skip applications**, which is rule 3 and nowhere else.

**Allocation is not ordered and not preserved.** Fusion exists to remove
allocations, so it plainly changes allocation traffic;
`docs/ALLOCATION-OBSERVABLE.md` already says the count is not portable
and `AL-06` constrains only *where in an expression* a box is charged.
Nothing here promises an allocation count, and a conformance case that
asserted one would be asserting an implementation detail.

## 3. Floating-point reassociation

**Normative.** A reduction over floats evaluates **strictly in index
order**. No fusion, vectorization or parallelization may reassociate it.
Tree reduction requires an explicit opt-in, and there is none today.

This follows from what is already promised rather than adding to it.
`docs/FLOAT-SEMANTICS.md` lists `a + b` as portable, meaning "the result
is identical across every backend for the same source bytes and same
inputs" (`FS-07`). Float addition is not associative, so a backend free
to reassociate would produce a different result from one that did not,
and the portability claim would be false. The claim already forbids
this; §3 only names the consequence for reductions.

The tree agrees in practice: `internal/ir/fold.go` leaves floats
untouched — "there's no portable way to round-trip every f32 bit-pattern
through the IR's float ops without surprising the user" — and its
integer reassociation is narrow and guarded (`OpAdd OpMul OpAnd OpOr
OpXor`, same kind and width, never shifts). There is no fast-math
surface anywhere in the tree, and this document does not add one.

**The opt-in, when it is wanted, is named per operation rather than per
build.** A `-ffast-math` style flag is the wrong shape here: it changes
the meaning of code the author did not write, including the stdlib's.
The shape to reach for instead is a separate reducer — `sum_unordered`
beside `sum` — so that a caller who wants a tree reduction asks for one
and a reader can see that they did. Naming it is deliberately left to
the issue that needs it; what is settled here is that the default is
strict and the opt-in is explicit and local.

## 4. Shape mismatch

**The shipped rule is truncation, and it is uniform.** Every
positionally-paired function in `internal/stdlib/std/array.fern` runs to
the shorter input and drops the longer tail: `zip`, `dot_f64`,
`distance_f64`, `add_f64`. None traps, none returns an `Option`, and
each says so in its doc comment ("truncated to the shorter input (the
usual zip-stops-at-shortest rule)").

**Normative, 1-D: that stays.** `AA-01` pins it. Changing a shipped,
documented, uniformly-implemented rule to a trap is a breaking change to
every caller, and it would be made for the benefit of a
multidimensional algebra that does not exist yet.

**Normative, for the multidimensional algebra when it arrives (#9734):
mismatch is an error, not a truncation.** The two answers differ and
that is deliberate, so here is the reasoning rather than an assertion:

- Truncation is defensible for `zip` because *the shorter input is the
  answer's shape* and the caller can see both inputs at the call site.
- It is indefensible for broadcasting, because there the shapes are
  derived — from a reshape, a transpose, an axis reduction — and a
  mismatch means the program computed a shape it did not intend. Silently
  producing the elementwise minimum of two wrong shapes turns a bug into
  a smaller, plausible-looking array. This is the failure mode every
  array language with silent truncation is known for.
- The non-goal list of #9727 forbids "hidden copies in structural
  operations without a documented materialization rule" for the same
  reason: a structural operation that quietly does something other than
  what was asked is the thing to avoid.

So the rule is *per operation*, and the distinction is whether the shape
was **written** or **derived**. `zip` of two arrays a caller named:
truncate. Any operation whose operand shapes come out of the algebra:
error.

## 5. Views, lifetime and storage

The failure mode this question names — a buffer reused under `own` while
a view of it is still live — **cannot be constructed today**, and the
reason is worth writing down because it is not a borrow checker.

- `T[]` is an owned counted array; `[T]` is "a non-owning `{data,len}`
  view holding no RC reference" (`docs/MODE-LATTICE.md`).
- A view may not escape the frame that owns its backing store: `E063`
  rejects returning a `[T]` that views function-local storage, and
  `E065` is the `str` sibling (`ML-03`, `ML-04`).
- Reuse is **dynamically guarded**, not statically proved: uniqueness is
  "deliberately a *dynamic* fact in Fern (the `__fern_rc_is_unique`
  gate) … not a static mode". A live view does not hold an RC
  reference, so it does not make the array non-unique — which is exactly
  the hazard.

**Normative.** An array that a live `[T]` view aliases is not eligible
for in-place reuse. Today this holds for a structural reason rather than
a checked one: a view cannot outlive its owner's frame (`E063`/`E065`),
and within that frame the owner binding is live, so
`docs/REUSE-CONTRACT.md`'s liveness taint already declines the site —
"any read of the donor after the construction site kills the pairing".

**That is a narrow guarantee and strided views widen the gap, which is
the real finding here.** It rests on a view being reachable only from a
binding in the same frame. The moment a view can be stored in a
structure, returned through an accessor, or held in an array of views —
all of which a strided-view model wants — the liveness taint stops
being a proxy for aliasing, and the dynamic uniqueness check will not
catch it because a view holds no count.

So #9734 does not inherit a solution. It inherits a **precondition**:
before `[T]` gains any way to outlive the frame it was taken in, either
views take an RC reference (paying for what they currently get free), or
the reuse analysis learns aliasing — the "cross-local reuse through
aliases needs the alias analysis the borrow model postponed" gap already
recorded in `REUSE-CONTRACT.md` as 5f. Whichever is chosen, it is chosen
before strided views ship, not after.

Worth knowing for anyone designing that surface: `[T]` is almost unused
today — "`[T]` appeared once in the whole stdlib against 53 uses of
`str`" — and nothing declares a `[T]` receiver, so method resolution on
views does not exist yet either. The surface is close to greenfield.

## 6. The specialization boundary

**Normative.** The algebra is recognized by **module-qualified stdlib
function identity**, not by bare name and not by a trait.

The precedent is `docs/ATLAS-PLATFORM-PLAN.md` §3.2, and it is
deliberately followed rather than improved on: kernels "enter the
language the same way the bit-count intrinsics do — as `__`-prefixed
compiler builtins that a readable stdlib function wraps, never as
surface syntax users are asked to write", threaded as "a name in the
builtin table → an `OpKind` → a lowering in each backend → an
interpreter implementation → a stdlib wrapper → tests."

The difference is which end is anchored. Atlas anchors the builtin and
lets the wrapper be ordinary Fern. The algebra has no builtin to anchor:
`map` is ordinary Fern today and should stay so, because the fallback
for an unfused pipeline is *running that code*. So the recognition is of
the wrapper, keyed on the resolved function's module and name, which a
user's own `map` in their own module does not collide with.

**Recognition by trait is the better long-run answer and is not
proposed yet.** A `Mappable`-style protocol would let a user's own
container join the algebra, which name recognition cannot. It is more
work and more honest, and it is the right thing to reach for once the
1-D algebra has shown what the operator set actually is. Doing it first
would mean designing a protocol against a guess.

**A stdlib function the algebra recognizes acquires a space contract.**
`ARRAY-PIPELINE-BASELINE-2026-09.md` §4 recorded that `fip` could not
reach the combinators at all — `E053` rejected the call to `map` before
`E068` was ever consulted, because `std/array` carries no annotation — so
"the space-contract system and the combinator library are disconnected
today, and 'fusion makes `map` allocation-free' would not by itself
connect them." Connecting them is part of recognizing an operator, not a
follow-up, and the connection is not an annotation on `std/array`: `map`
as written allocates, and its contract is conditional on the call site
(an `own` receiver, a capture-free element function). So the contract is
carried by the CALLER's claim and verified at the IR: E053 admits
`xs.map(f)` on an `own` receiver, and E068 counts a `map` that R7 does
not write through its donor, naming the taint (#9733). An operator joins
the algebra when its IR verdict is what E068 reads — that is what
"annotated" means for a combinator whose contract depends on how it is
called.

**Recognition is of the resolved callee, not of the mangled name.**
`__method_Array_<verb>` is the mangling of any receiver method on arrays;
a program that never imports `std/array` may declare its own `map` and it
is not the algebra's. The IR derives the verb table per program: the free
function `array__<verb>` (a prefix modload reserves) and the method
spelling only where its body delegates to that free function, which is
what `std/array`'s wrappers do and what a user's method cannot.

## 7. Diagnostics

**Normative.** Whether a pipeline fused, and if not which operator or
position declined it, is reportable on demand. The shape is
`-append-report`'s, which already "print[s] every `.append` site … and
whether the compiler grew the array in place or copied it, **with the
rule that decided**".

That last clause is the requirement. A report that says "did not fuse"
is not the answer; the answer names the rule — not statically resolved
(§1), would skip an application of a function that can abort (§2),
operator outside the algebra (`ITERATOR-FUSION-CONTRACT.md` §2).

This is the same stance as the fusion contract's clause 4, "failure is
visible, not silent", and as `REUSE-CONTRACT.md`'s specified-not-
best-effort framing.

**It is built.** `fern -array-report FILE.fern` prints, per pipeline, the
recognized plan, whether it fused, how many of its stages materialize an
array, and — where a chain stopped — which rule stopped it:

```
run:3:26  map(n->n)
            not fused: one stage is not a chain, and its result is the value; 1 stage(s) materialize unfused
            chain ends here: the intermediate is read again, so this is not one traversal
            map        elementwise  __closure_lambda_1
```

A refusal that belongs to one stage names it, and names the element
function when the reason is about that:

```
run:4:44  map(n->n) -> map(n->n) -> scan(n->n) -> fold(n->1)
            not fused at stage 3 (scan): neither map nor filter; 3 stage(s) materialize unfused
```

Naming the stage is the point. "Neither map nor filter" on a four-stage
chain sends the reader back to count stages themselves, which is the work
the report exists to save. A refusal no single stage owns — the array is
not held in a local, say — names none rather than blaming the first.

Each stage also says where its buffer came from, which is #9732's second
question:

```
map        elementwise  __closure_lambda_1
  fresh buffer: the combinator borrows its array, so there is no donor to reuse
fold       reduction    __closure_lambda_2
  no buffer: a reduction produces a value
```

The verdicts come from the planner that performs R7's in-place rewrite
(`docs/REUSE-CONTRACT.md`, #9733), so `reused` is printed exactly where the
pass writes through the `own` parameter and every refusal names the taint
that declined it — `receiver-not-own-param`, `element-fn-captures`,
`shape-change`, and the rest of the closed set in
`internal/ir/array_storage.go`. A hardcoded sentence would go on being
printed after the fact it describes stopped being true, which is what the
report did about `map` for the day between R7 landing and its verdicts
being read here. `internal/ir/array_storage_test.go` pins one verdict per
taint.

```
map        elementwise  __closure_lambda_1
  donated buffer: written through the `own` parameter (R7)
```

The same verdict is what E068 reports when a `fip` / `fbip` function's
`map` is declined, so "why did this allocate under my space claim" and
"why did this allocate" have one answer.

`FERN_ARRAY_REPORT=1` adds a histogram, in three sections: why each chain
was not FUSED (#9731), where each stage's BUFFER came from (#9732), and
where each chain STOPPED being one chain (#9730). Both sets are **closed** with stable tags, for the reason
`FERN_SSA_REPORT` is: the set of things declined is the coverage checklist
for widening the algebra, and a tally of free-text strings cannot be
counted. Every reason prints a row even at zero, so a reason that stops
firing shows as a zero rather than as a line nobody notices went missing.

```
array pipelines: 4 (4 with more than one stage), 8 stages, 4 materializing
fused: 0 (#9731)
  single-stage               0
  sink-not-a-reduction       0
  stage-not-elementwise      1
  element-fn-unresolved      0
  element-fn-effectful       1
  receiver-not-a-slot        1
  element-width-unsupported  1
  ...
chains stopped by (#9730):
  complete                   4
  ...
```

The verdict comes from the fusion planner itself (#9731), not from a
constant: a report that claimed a fusion the backend did not perform would
be worse than no report. The histogram is read from the report mode rather
than from every compile, because a per-build hook would cost a print in
five backends for a number most builds do not want.

## The claims

| | Claim |
| --- | --- |
| **AA-01** | Positional pairing truncates to the shorter input. `zip`, `dot_f64`, `distance_f64` and `add_f64` run to the shorter length and drop the longer tail; a length mismatch is not an error and not an `Option`. |
| **AA-02** | A floating-point reduction evaluates in index order. `sum` over an `f64[]` gives the strictly left-to-right result, so a reassociating implementation is detectable and forbidden. |

Both are pinned; see `spec/semantics.md`.

The rules in §1, §2, §5, §6 and §7 are still **not** index claims, and
#9731 building the pass did not change that — for a reason worth stating
rather than leaving as an omission.

§1 is now enforced and tested (`internal/ir/array_fusion_test.go`), but it
is enforced as a *refusal*: a chain with an effectful element function is
left unfused. So §1 and §2 together make the fused and unfused programs
indistinguishable, which is the point — and a conformance case can only
observe a difference. There is nothing for one to assert that a compiler
ignoring the rules would fail. §5, §6 and §7 are compiler-internal in the
same way.

That is the opposite of `ALLOCATION-OBSERVABLE.md`'s path, where building
the observable is what made the claims checkable. Here the guarantee is
that nothing is observable, so the gates are the IR and e2e tests, not the
conformance corpus.

## What this does not settle

**Whether array programming is in scope at all is recorded stale.**
`docs/LANGUAGE-DIRECTION.md`'s "Things deliberately NOT cribbed" still
rejects "Odin's array swizzling / SoA programming" as "Cute for gamedev,
off-target for CLI / edge". That reasoning predates the general-purpose
statement in `CLAUDE.md` and the move to reference counting, and it sits
three lines below the entry for "Roc's runtime refcounting", which
carries a **REVERSED** marker for exactly the same reason: the
short-lived-process assumption stopped holding. #9727 is array
programming. The entry needs re-deciding in that document by whoever
owns the direction, and this one does not quietly assume the answer.

Parallel and accelerator backends are deliberately not filed (#9727
phase 5), and nothing here is written to accommodate them: §3's strict
ordering is the rule a parallel reduction would most want relaxed, and
relaxing it is a decision with a name on it, not a consequence of
building a fusion pass.
