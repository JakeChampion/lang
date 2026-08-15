# Behavioural semantics

Status: normative index. The rules that say what an accepted Fern
program *does* live as prose in `docs/`, written down as policy notes
before this directory existed. This file is the index of the claims
those notes make and of which conformance case pins each one.

`spec/diagnostics.md` is the sibling for rejections: it says which
programs the front-end refuses. This one says what the accepted ones
are guaranteed to do.

## Why an index and not a rewrite

The policy docs are living documents — they carry implementation
notes, the reasoning behind a choice, and corrections when the choice
turned out to have been described wrongly. Copying their rules here
would produce two statements of the same thing and one of them would
go stale. So the docs stay where they are, and this file adds the one
thing prose cannot do for itself: a machine-checked link from each
claim to a program that demonstrates it.

## Why that link is worth having

`docs/CLOSURE-CAPTURE.md` is the argument. It stated for months that
closures capture scalars **by value** — a snapshot taken at closure
creation. Every compiled backend had captured by reference since
#2896, deliberately, to match the interpreter. The document was not
describing a bug; it was describing a language that no implementation
had implemented, and nothing could tell, because a prose claim with no
test attached is unfalsifiable. It was corrected in #5479 by editing
the doc.

A claim in this index cannot fail that way silently: the case named in
the last column has to exist, has to name the claim back, and has to
pass on every backend it opts into.

## How the link is checked

`TestSemanticsIndexIsAccurate` (`internal/e2e`) enforces, in both
directions:

- every claim ID is unique, and its doc exists;
- every named case exists under `conformance/cases`;
- every named case carries a `// spec: <ID>` comment naming the claim
  back, so a case cannot be repurposed out from under a claim, and a
  claim cannot point at a case that is about something else;
- every `// spec:` marker in the corpus names a claim that exists here,
  so a marker cannot outlive the claim it was written for;
- every doc in `spec/README.md`'s normative-prose table appears here
  with at least one claim;
- the counts in the paragraph below match the table.

The cases themselves are run by `TestFernFixtures` like any other, so
"pinned" means the behaviour is checked on every backend the case opts
into, not merely that a file exists.

**35 of 38** claims are pinned by a conformance case. The remaining
**three are freedoms** — see below. There are no gaps left.

## Freedoms are not gaps

Three rows are marked `n/a — freedom` rather than `—`. These are
places where the language deliberately declines to guarantee anything:
`docs/FLOAT-SEMANTICS.md` under-specifies the NaN bit-pattern, denormal
handling, and whether `-0.0` survives arithmetic, on the argument that
cross-backend bit-equality at the IEEE edges costs canonicalisation on
every NaN-producing op and buys nothing for the programs Fern is for.

A freedom cannot be pinned by a conformance case, because a case
asserts an output and the whole content of the claim is that no
particular output is owed. The distinction is worth keeping visible:
a `—` is work someone should do, and a `n/a` is a decision someone
already made. Collapsing them would turn three deliberate choices into
three apparent oversights, which is what a bare `—` would have made
of them.

## Claims

| Claim | Doc | Rule | Pinned by |
| --- | --- | --- | --- |
| `AB-01` | `docs/ARRAY-BOUNDS.md` | Reading an array past its end aborts | `oob_index_read` |
| `AB-02` | `docs/ARRAY-BOUNDS.md` | A negative index aborts | `oob_index_negative` |
| `AB-03` | `docs/ARRAY-BOUNDS.md` | Writing past the end (`xs.with(i, v)`) aborts | `oob_index_write` |
| `AB-04` | `docs/ARRAY-BOUNDS.md` | Slice construction outside `0 <= lo <= hi <= len` aborts | `oob_slice_range` |
| `AB-05` | `docs/ARRAY-BOUNDS.md` | `lo == hi == len` is a legal empty slice | `slice_at_length` |
| `IS-01` | `docs/INTEGER-SEMANTICS.md` | `+ - * <<` wrap at the operand's width | `int_wrap` |
| `IS-02` | `docs/INTEGER-SEMANTICS.md` | Shift counts are masked (`& 31` / `& 63`) | `int_shift_count_masked` |
| `IS-03` | `docs/INTEGER-SEMANTICS.md` | `>>` is arithmetic for signed operands, logical for unsigned | `int_shift_count_masked` |
| `IS-04` | `docs/INTEGER-SEMANTICS.md` | `x / 0 == 0` — division never traps | `int_div_rem_by_zero` |
| `IS-05` | `docs/INTEGER-SEMANTICS.md` | `x % 0 == x` | `int_div_rem_by_zero` |
| `IS-06` | `docs/INTEGER-SEMANTICS.md` | `MIN / -1 == MIN` — no overflow trap | `int_min_arithmetic` |
| `IS-07` | `docs/INTEGER-SEMANTICS.md` | `MIN % -1 == 0` | `int_min_arithmetic` |
| `IS-08` | `docs/INTEGER-SEMANTICS.md` | `+\| -\| *\| <<\|` clamp to the type's `[MIN, MAX]` | `int_saturating_ops` |
| `IS-09` | `docs/INTEGER-SEMANTICS.md` | `+? -? *? /? %? <<? >>?` yield `None` on overflow, zero divisor, or out-of-range count | `int_checked_ops` |
| `IS-10` | `docs/INTEGER-SEMANTICS.md` | A saturating operator on `usize` is rejected (`E009`) | `sat_usize_rejected` |
| `IS-11` | `docs/INTEGER-SEMANTICS.md` | A saturating operator is rejected inside a `const` initializer | `sat_const_rejected` |
| `FS-01` | `docs/FLOAT-SEMANTICS.md` | Float-to-int conversion of `NaN` yields `0` | `float_to_int_saturating` |
| `FS-02` | `docs/FLOAT-SEMANTICS.md` | Out-of-range float-to-int saturates to the destination's min / max | `float_to_int_saturating` |
| `FS-03` | `docs/FLOAT-SEMANTICS.md` | In-range float-to-int truncates toward zero | `float_to_int_saturating` |
| `FS-04` | `docs/FLOAT-SEMANTICS.md` | The NaN bit-pattern is not specified | n/a — freedom |
| `FS-05` | `docs/FLOAT-SEMANTICS.md` | Denormal handling is not specified | n/a — freedom |
| `FS-06` | `docs/FLOAT-SEMANTICS.md` | Whether `-0.0` survives arithmetic is not specified | n/a — freedom |
| `FS-07` | `docs/FLOAT-SEMANTICS.md` | Ordinary `+ - * /` and comparisons are portable across backends | `float_pythagoras` |
| `CC-01` | `docs/CLOSURE-CAPTURE.md` | A mutation of a captured scalar inside a closure is visible outside | `closure_capture_shared_cell` |
| `CC-02` | `docs/CLOSURE-CAPTURE.md` | A write to the outer variable is visible inside the closure | `closure_capture_shared_cell` |
| `CC-03` | `docs/CLOSURE-CAPTURE.md` | Assigning a reference-typed capture is rejected (`E049`) | `diag_e049` |
| `ML-01` | `docs/MODE-LATTICE.md` | A borrowed value cannot be passed to an `own` parameter (`E051`) | `diag_e051` |
| `ML-02` | `docs/MODE-LATTICE.md` | A `fip` function may not allocate (`E053`) | `diag_e053` |
| `ML-03` | `docs/MODE-LATTICE.md` | A `[T]` view of function-local storage may not be returned (`E063`) | `diag_e063` |
| `ML-04` | `docs/MODE-LATTICE.md` | A `str` view of a function-local string may not be returned (`E065`) | `diag_e065` |
| `ML-05` | `docs/MODE-LATTICE.md` | Using an owned parameter after it is consumed is rejected (`E050`) | `diag_e050` |
| `ML-06` | `docs/MODE-LATTICE.md` | A `fbip` function that allocates without a donor to reuse is rejected (`E068`) | `diag_e068` |
| `MC-01` | `docs/MUST-CONSUME.md` | A `@must_consume` value unconsumed on some path is rejected (`E067`) | `diag_e067` |
| `MC-02` | `docs/MUST-CONSUME.md` | An `own` parameter is the declared sink, exempt from `E067` | `must_consume_own_sink` |
| `AL-01` | `docs/ALLOCATION-OBSERVABLE.md` | A loop that reclaims what it allocates does not grow the fresh-allocation high-water mark with the round count | `alloc_flat_under_reclaim` |
| `AL-02` | `docs/ALLOCATION-OBSERVABLE.md` | A loop that retains what it allocates does grow it with the round count | `alloc_grows_when_retained` |
| `AL-03` | `docs/ALLOCATION-OBSERVABLE.md` | A loop that allocates a closure environment per round reclaims it too | `alloc_flat_closure_env` |
| `AL-04` | `docs/ALLOCATION-OBSERVABLE.md` | A loop that only READS a container — a map lookup, hit or miss — reclaims what the read allocated | `alloc_flat_map_get` |

## Every gap this index opened with is now closed

`IS-10`, `IS-11`, `ML-05`, `MC-01` and `MC-02` were all *rejections*
exercised only by a Go test under `internal/checker`. That is coverage
of `internal/`, and `docs/NATIVE-CONVERGENCE.md` makes the self-host
compiler the definition once the freeze preconditions (#4451) go green
— at which point a Go test measures the wrong implementation. `E050`
and `E067` were also two of the three codes `spec/diagnostics.md`
listed as unpinned, so those rows closed in both indexes at once.

`ML-06` was the interesting one, because it was not unpinned for want
of writing a case. `E068` is reported by `internal/ir/fip_verify.go`
during *lowering*, after the checker has already accepted the program,
and a case with an `expected.error` file stops at the type check — so
the format itself could not express the claim. Rather than move the
`fbip` check forward into the checker to suit the test format, the
format grew an `expected.lowering-error` sidecar that asserts both
halves: the front end must accept the program and lowering must reject
it. A program the checker already rejects is reported by name and told
to move to `expected.error`, because which stage rejects a program is
exactly what these cases pin.

The lesson is worth keeping: an unpinnable claim is sometimes a fact
about the harness rather than about the language, and writing down
which of the two it is — rather than a bare `—` — is what made the
next step obvious.

## The store, and the first thing said about it

`AL-01` / `AL-02` are the first claims here that are about the **store**
rather than about values. Allocation was not unmeasured before them —
`TestX86_64AllocScaling` bounds asymptotics and
`TestSelfHostAllocDifferentialX86_64` compares the two compilers — but
both are Go tests on one backend, so neither survives the freeze and
neither can see a backend that allocates unlike the others. The
*corpus* pinned allocation behaviour nowhere at all, and every leak
investigation under #6127 had to build a bespoke differential harness
before it could measure anything.

They are deliberately weak: they say only that a reclaiming loop's
fresh-allocation high-water mark does not grow with the round count and
a retaining one's does. That is far short of a store semantics, and
`docs/ALLOCATION-OBSERVABLE.md` says which questions it leaves open. But
it is the difference between a property that can be pinned and one that
cannot, and the first thing it found was a real leak: the wasm backend
never reclaims a short-lived string, consuming exactly 32 fresh bytes
per iteration where both natives are flat (#6423).

## What is still not here

The claims above are the ones the policy docs happen to state. They are
not a semantics: there is no evaluation order, no typing rule, no
memory model, and — beyond the two allocation-shape claims — no
statement of when a value is freed.

Read this file as a list of the promises that have been written down,
not as the set of promises Fern makes.
