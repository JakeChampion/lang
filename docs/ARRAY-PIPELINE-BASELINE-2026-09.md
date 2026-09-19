# The array pipeline baseline, September 2026

What #9728 asked, measured. The programs are `examples/array_pipeline/*.fern`,
the measurement is `scripts/array-pipeline-baseline`, and
`internal/e2e/array_pipeline_baseline_test.go` keeps the claims below from
rotting.

**These are PRE-FUSION numbers.** #9731 has since landed
(`internal/ir/array_fusion.go`), so the allocation figures below are now
reproduced only with `FERN_NO_ARRAY_FUSION=1` — which is how the gate still
asserts them, alongside what the pass changed. Fused, `map.map.reduce` falls
from 23 allocator calls per round to 1 and `filter.map.reduce` from 19 to 1,
the remainder being `reduce`'s own `Option` box rather than anything linear in
the input. On retired instructions the combinator-to-loop ratios fall from
4.69x to 1.21x and from 2.84x to 0.98x. The warning below about the indirect call turned out not to bind:
fusion runs before `Defunctionalise`, which then inlines the element functions
outright, so the fused loop makes no indirect call at all.

This is the prerequisite for #9727. `docs/ITERATOR-FUSION-CONTRACT.md` already
names its own trigger condition — "a real workload demonstrates the eager
combinators allocating measurably in a hot path" — and nobody had produced the
measurement. Read `docs/ALLOCATION-OBSERVABLE.md` first for what the allocation
numbers mean and which half of them is portable.

**Verdict in one line: the gap is real and it is worth closing, but it is not
made of the one thing the design note assumed.** The intermediate arrays are the
larger term on native-built code and the per-element indirect call is a solid
quarter to two fifths of the rest — and on SELF-HOST-built code, which
`docs/NATIVE-CONVERGENCE.md` makes the definition, that second term is half to
three quarters. A fusion pass that removes the intermediates and leaves the
calls unspecialised recovers well under half of what is on the table, and less
than that on the compiler this is heading toward.

## The three programs

Each isolates one question, and each ships three variants that compute the
**same answer** — that agreement is the experiment's differential test, and
every variant of every program on every backend below reported an identical
checksum.

| program | the chain | the question |
| --- | --- | --- |
| `map_map_reduce` | `xs.map(f).map(g).reduce(h)` | producer/consumer fusion at its simplest: same cardinality, no control flow |
| `filter_map_reduce` | `xs.filter(p).map(g).reduce(h)` | variable cardinality and a branch per element, where a fixed-trip-count scheme breaks |
| `own_map_inplace` | `own xs` through a same-shape `map` | not fusion: can the result BE the input's buffer? |

The first two carry three variants: the combinator chain, a `closure_loop` that
does one traversal with no intermediates but still reaches `f`/`g`/`h` through
function values, and a `loop` with the arithmetic written inline. Reporting only
the first and last leaves the intermediates and the calls summed and
unattributable, which is the whole thing #9728 asked to separate.

**`closure_loop` had to be built carefully and the first version of it was
wrong.** Assigning a lambda straight to a local lets it be inlined into the
loop — the emitted code for that form contains no call at all — so the variant
measured exactly what `loop` did and the first round of numbers attributed 5% to
the indirect call. The element functions now reach the loop through a picker
selected by a command-line-derived value, which nothing at compile time can
fold, and the emitted loop contains the same `call r11` that `map` and `reduce`
make through their own `f` parameter. The real figure is 26-39%.

## How it is measured

Three instruments, in descending order of how much weight they carry.

**Allocation counters** (`__heap_alloc_count()`, `__heap_bump_bytes()`) are
deterministic for a given backend, program and `n`, so a row that differs
between two backends is a real difference. Each program is run at `n` and `2n`.

Two readings, not one, because they answer different questions. The **cold**
reading brackets the first pass, before the freelist holds anything to hand
back; the **steady** reading covers every later round. The intermediate arrays
are tens of kilobytes in the first and *nothing* in the second — the allocator
recycles the same blocks and the bump cursor never moves again. A report
carrying only the steady bytes would have concluded the combinators allocate
nothing at all.

**Retired instructions under callgrind**, the instrument
`scripts/perf-bench` already uses. This is the only runtime number here a
percentage can be read off. valgrind cannot run under qemu-user, so the counts
below are arm64-linux, executed natively inside `scripts/devbox` — which the
image now installs valgrind for.

**Wall time** is recorded and is not the basis of any claim in this document.
The spread between the fastest and slowest run of an *unchanged* binary was
measured at over 1.6x on an unpinned laptop, which is larger than most of what
is being looked for; an early draft of this report had the `filter` pipeline
running *faster* than its hand-written loop on that evidence, which the
instruction counts show is the reverse of the truth.

## What it found

### 1. The combinators allocate per stage, and only the count shows it

`map_map_reduce`, n=4096. Identical on all four backends, to the digit.

| variant | cold allocs | cold fresh bytes | steady allocs (200 rounds) | steady fresh bytes |
| --- | ---: | ---: | ---: | ---: |
| `pipeline` | 25 | 101,408 | 5,000 | **0** |
| `closure_loop` | 0 | 0 | 0 | 0 |
| `loop` | 0 | 0 | 0 | 0 |

`filter_map_reduce` is the same shape, smaller: 21 cold allocs, 15,392 cold
bytes, 4,200 steady allocs. The predicate keeps one element in three, so both
intermediates hold 1,364 elements rather than 4,096 — fewer regrows, and enough
of them recycled inside the single pass that the cold byte figure lands well
below a third of pipeline 1's rather than at it.

So: **25 allocator calls per round, about 24.8 fresh bytes per input element on
the cold pass, and zero fresh bytes forever after.** The count is per geometric
regrow, not per element, because `map` and `filter` both build their result by
`append` into an array that starts empty. Doubling `n` adds one regrow per
stage: 5,000 steady allocations at n=4096, 5,400 at n=8192.

The zero in the last column is the finding, not a null result. It is exactly the
case `docs/ALLOCATION-OBSERVABLE.md` describes — "a reclaiming loop is flat in
bytes and linear in calls" — and it means an allocation claim about this code
made from the bump mark alone reads as green.

### 2. The dominant cost is the intermediates, and the calls are not noise

arm64-linux, retired instructions over 10 rounds at n=4096.

| | `map_map_reduce` | `filter_map_reduce` |
| --- | ---: | ---: |
| `pipeline` | 11,501,515 | 5,355,498 |
| `closure_loop` | 4,977,192 | 3,352,467 |
| `loop` | 2,632,371 | 2,075,320 |
| pipeline ÷ loop | **4.37x** | **2.58x** |
| of the gap: intermediates + extra traversals | **73.6%** | **61.1%** |
| of the gap: per-element indirect call | **26.4%** | **38.9%** |

The emitted code says the same thing directly. Instructions in the inner loop
body, per element:

| loop | x86-64 | arm64 | calls per element |
| --- | ---: | ---: | --- |
| `array.map` | 56 | 51 | indirect `f`, `__fern_arr_push_grow`, `__fern_arr_dec` |
| `array.filter` | 60 | 54 | the same three |
| `array.reduce` | 34 | 29 | indirect `f` only |
| `closure_loop` | 50 | 45 | three indirect, no materialization |

A traversal that only calls indirectly and accumulates costs 34 x86-64
instructions per element; one that also materializes costs 56. Materialization
is therefore about 22 instructions and two calls per element — and
`map(f).map(g).reduce(h)` spends 56 + 56 + 34 = **146 instructions per element
across three loops** where the hand-written single traversal spends 50.

The practical consequence for #9730/#9731: **fusing the traversals without
specialising the element calls leaves roughly a quarter to two fifths of the gap
in place.** `docs/ITERATOR-FUSION-CONTRACT.md` already requires both — its
compositional guarantee names "no heap allocation, **no unspecialised calls per
element**" — and this is the measurement saying the second half is not a
rounding error.

One thing that is *not* a term: the bounds check. `internal/parser/bounds_elide.go`
recognises `while (i < xs.len())` syntactically, and a loop bounded by a
separate local — `var n = xs.len(); while (i < n)`, the reflex optimisation —
keeps the check. The two spellings emit visibly different code and cost the
same: over a 100,000-element sum repeated 50 times, 117,001,246 retired
instructions for the hoisted-and-checked form against 116,997,604 for the
elided one, a difference of **0.003%** across five million element visits. The
check and the length reload it replaces are worth the same. The hand-written
baselines here use the elided spelling so that they and the combinators' own
`for x in xs` index on the same terms, not because it bought anything.

### 3. `own` plus a same-shape `map` does not reuse the donor

`own_map_inplace`, n=4096, native compiler. Again identical on all four backends.

| variant | cold allocs | cold fresh bytes | steady allocs | Ir (arm64, 10 rounds) |
| --- | ---: | ---: | ---: | ---: |
| `map_own` — `own xs` into `xs.map(f)` | 12 | 52,224 | 2,400 | 4,953,503 |
| `with_own` — hand-written `own` loop, `fip` | 0 | 0 | **0** | 3,177,531 |
| `with_borrowed` — same loop, borrowed receiver | 1 | 49,152 | 200 | 3,270,338 |

**No R-shape fires, and nothing taints the site.** `std/array`'s `map` builds
its result from `[]` and appends, so there is no construction the donor's box
could be paired with — the Perceus reuse token in `docs/REUSE-CONTRACT.md`
threads from a dead donor's drop to an *allocation site*, and a growing append
loop has one per regrow that is structurally unrelated to the input buffer.
`own` licenses the consumption and nothing collects on it. This is #9733's
whole job.

Two things worth having measured rather than assumed:

- **The `fip` claim holds.** `with_own` is annotated `fip`, E068 accepts it, and
  it measures zero allocations across every backend. The verifier and the
  allocator agree.
- **Ownership buys less than it looks.** `with_borrowed` pays one copy-on-write
  copy per round — one allocation, 49,152 cold bytes — and costs 2.9% more
  instructions than the owned loop. One 32 KB `memcpy` amortised over 4,096
  elements is cheap. The combinator, by contrast, costs **51-56% more** than
  either. The win here is in not rebuilding the array by append, not in
  ownership as such.

### 4. `fip` cannot reach the combinators at all — E053, not E068

#9728 asks whether `fip` on the third pipeline passes E068. It cannot get that
far:

```
error[E053]: `fip` function "via_map_own" may not call method "map" (not proven allocation-free)
    return xs.map((x: i64): i64 => elem_f(x));
                 ^
```

`fbip` gives the identical diagnostic. `std/array`'s combinators carry no
space annotation, so a `fip` function may not call one whatever it does inside
— which means that even a `map` that *did* reuse its donor would remain
uncallable from annotated code until `std/array` is annotated too.

That is a finding for #9729 and #9732 rather than a defect: the space-contract
system and the combinator library are disconnected today, and "fusion makes
`map` allocation-free" would not by itself connect them.

### 5. The two compilers agree on answers and disagree on cost — and the split moves

Measuring this at all needed #9743 fixed first: `mono_infer`'s
generic-array-method arm handed back the erased type variable `U[]` as though it
were a type, so `xs.map(f).reduce(g)` did not lower and the self-hosted compiler
could not build two of these three programs on any target. With that repaired,
every variant of every program agrees with native's checksum, and the
interesting column opens up.

**The decomposition of §2 shifts, in the direction that matters.** Retired
instructions, arm64-linux, 10 rounds at n=4096:

| | ratio vs loop | intermediates + traversals | per-element indirect call |
| --- | ---: | ---: | ---: |
| `map_map_reduce`, native | 4.37x | 73.6% | 26.4% |
| `map_map_reduce`, **self-host** | 4.77x | 48.8% | **51.2%** |
| `filter_map_reduce`, native | 2.58x | 61.1% | 38.9% |
| `filter_map_reduce`, **self-host** | 2.20x | 27.4% | **72.6%** |

The gap a fusion pass would close is comparable under both compilers, but what
it is *made of* is not: the per-element call is a quarter to two fifths of it on
native-built code and **half to three quarters** on self-host-built code. The
self-host IR has none of native's defunctionalise / elide passes (#6638), so a
call native devirtualises stays indirect there — and a capture-free closure
merely *returned from a function* costs an environment allocation per call,
which is why `closure_loop` reads zero steady allocations native-built and three
per round self-host-built. Minimal repro on #6638.

Since `docs/NATIVE-CONVERGENCE.md` makes the self-host compiler the definition
once the freeze preconditions go green, the half of the fusion contract about
unspecialised calls is the LARGER half on the compiler this is heading toward,
not the smaller one §2's native figures suggest.

On `own_map_inplace` the two compilers agree on the checksum
and on the allocation SHAPE — `with_own` is zero under both, so the self-host
compiler reaches the same allocation-free steady state, and `with_borrowed`
pays exactly one copy per round under both. They do not agree on the volume,
which `docs/ALLOCATION-OBSERVABLE.md` says is per-implementation and must never
be asserted: the self-host compiler spends one more allocator call on the
combinator (13 cold, 2,600 steady against 12 and 2,400) and hands out fewer
fresh bytes for it (34,896 against 52,224), which is a different regrow policy
rather than a defect.

What differs much more is the code:

| variant | native Ir | self-host Ir | self-host ÷ native |
| --- | ---: | ---: | ---: |
| `map_own` | 4,953,503 | 5,544,368 | 1.12x |
| `with_borrowed` | 3,270,338 | 5,895,777 | 1.80x |
| `with_own` | 3,177,531 | 6,164,215 | **1.94x** |

**The ordering inverts.** Under the native compiler the owned in-place loop is
the cheapest of the three; under the self-hosted one it is the most expensive,
and the combinator the cheapest. A programmer optimising against self-host-built
output would be led to the opposite conclusion from a programmer optimising
against native-built output — on the same three functions. That belongs to
goal 2 and to `docs/rc-log/`, not to this epic, but it is the sort of divergence
the epic would otherwise design on top of.

The self-hosted compiler still cannot build any of the three for wasm: its
component wrapper refuses a program importing both `args()` and the clock,
either alone being fine. Not new and not caused by these programs —
`examples/fip/packet_fip.fern` fails the same way — so the self-host wasm column
is the one gap this round leaves open.

## Verdicts on #9728's questions

1. **Do the eager combinators allocate per stage, and by how much?** Yes. One
   allocator call per geometric regrow per stage — 25 per round for two `map`
   stages at n=4096 — and ~24.8 fresh bytes per input element on the cold pass.
   In the steady state the byte volume is **zero**, because the freelist
   recycles the intermediates. Only the count sees it.
2. **Is the dominant cost the intermediates, the closure call, or neither?**
   The intermediates, at 74% (same cardinality) and 61% (with a filter) of the
   gap over a hand-written loop. The per-element indirect call is the remaining
   26% and 39% and is not negligible.
3. **Does `own` plus a same-shape `map` reuse the donor?** No, and not because
   the site is tainted: `map` builds a fresh array by append, so there is no
   paired construction for any R-shape of `docs/REUSE-CONTRACT.md` to fire on.
4. **Does `fip` pass E068 on that pipeline?** The question cannot be reached.
   E053 rejects the call to `map` first, because `std/array` carries no space
   annotation. The hand-written `own` loop does pass E068 and does measure zero.
5. **Do the two compilers agree?** On answers, yes — every variant of every
   program, once #9743 was fixed; before it, two of the three did not compile
   under the self-hosted compiler at all. On cost, no: instruction counts differ
   by up to 1.94x, the ranking of pipeline 3's three variants inverts, and the
   share of the gap owed to the per-element call roughly doubles.

## What this means for #9727

The epic's premise survives, with one correction and one caveat.

**It survives.** The eager combinators cost 2.6x to 4.4x a hand-written loop on
these shapes and materialize an intermediate per stage that is linear in the
input. #9728 allowed for the outcome that "the eager combinators are already
adequate in the shapes that matter"; they are not.

**The correction is where the cost lives.** The design note treats the
intermediate arrays as the thing to remove. They are the larger term but not the
whole one, and a pass that fuses traversals while leaving `f` reached through an
opaque function value per element stops between a quarter and two fifths short.
#9730 and #9731 should be read as needing both halves — which is what
`docs/ITERATOR-FUSION-CONTRACT.md` already says, and which this measurement now
prices.

**The caveat is that measuring only native-built output would aim the work
wrongly.** `docs/NATIVE-CONVERGENCE.md` makes the self-host compiler the
definition once the freeze preconditions go green, and on self-host-built code
the per-element call is half to three quarters of the gap rather than a quarter
to two fifths — because the self-host IR has none of native's defunctionalise
and elide passes (#6638). So the call-specialisation half of
`ITERATOR-FUSION-CONTRACT.md` is not a refinement to do after fusion lands; on
the compiler this is heading toward it is the bigger win of the two. Pipeline
3's ranking inverting between the compilers is the same warning in a second
place, and belongs to goal 2.

## Reproducing

```
make build                          # bin/fern
make selfhost-cli                   # bin/fern-selfhost
scripts/array-pipeline-baseline report.txt
```

The script measures what the host can execute and records what it cannot as a
`# skipped:` line naming the missing piece. For the Linux targets and the
callgrind instruction counts on a Mac:

```
scripts/devbox bash -c '
  go build -o /tmp/fernbin/fern ./cmd/fern
  /tmp/fernbin/fern -target arm64-linux -o /tmp/fernbin/fern-selfhost examples/self_host/fern.fern
  FERN_NATIVE_BIN=/tmp/fernbin/fern FERN_SELFHOST_BIN=/tmp/fernbin/fern-selfhost \
    scripts/array-pipeline-baseline /tmp/report.txt'
```

The binaries go to `/tmp` inside the container because `bin/` is the host's, and
a Linux `bin/fern` written over it leaves the host without one.
