# 2026-09-06 — native's field-place mutation analysis catches up with the port

`internal/ir`'s `fieldPlaceAppendCopies` is the oracle the self-host's
`field_append_inplace_sites_of` was ported from, and the port had since moved
twice in the same direction (#8523). Native now has both arms, and the function
is `fieldPlaceMutationCopies`: it decides `.append` AND `.with`.

## Arm 2 — the no-overlap arm is sequenced

The third arm was "no overlapping read ANYWHERE in the body", whatever the order
of the reads. It now excuses a read of the site's own place in LENGTH or INDEX
position (`o.xs.len()`, `o.xs[i]`) from a top-level statement the site runs
after: both yield a scalar computed before the mutation. Only those two
positions, and only for a root no defer or lambda mentions — those run after the
statement that spells them, so "textually earlier" would stop meaning "earlier".

**Flow-insensitivity was under-admitting, uniformly.** The arm required ZERO
overlapping reads, which is order-independent and strictly stronger than the
ordered rule, so it could not admit anything ordering forbids. Measured over
`examples/self_host/fern.fern` (4,883 append sites, 30 copying): of the 22
field-receiver refusals, **21 are a bare read of the root** — a whole-container
read or a container-binding capture, which the prefix rule does not touch — and
one is `asmcore.add_string_lit`'s `s.string_lits[i]`, an index read inside the
dedup loop that precedes the append. Admitting the sequenced reads therefore
moves **no append decision in the whole corpus**; what it unblocks is the `.with`
beside them, which is the arm below.

## Arm 1 — the field-place `.with` stores in place

Native had no `.with` arm in this analysis, but it was not cloning every
field-receiver `.with`: `computeFieldOwnMoves` (#8186) already claims the
superseded-field shape `x = S { ...x, f: x.f.with(i, v) }` and moves the field
out. What was missing is every OTHER host — the body-scope and return-position
forms, which `computeArraySetIncs` refused as a projection out of a live
container and inc'd into the copy path.

Over the self-host corpus that is exactly **one** site: `treeshake.refset_add`,
the hash-index shape the issue named (the ten others that this admission reaches
are `x86_add_label` / `arm64_asm_label` / `peep_set`, all already in place via
#8186). Isolated on the shape itself — 200k inserts into a 4093-slot bucket
table, x86-64 — it is the whole cost: **0.102 s → 0.008 s**.

### The lowering, and why it is not the port's

`emitCowInplaceFieldMove` is `emitCowInplace` with rc bookkeeping in each branch:

    if is_unique(arr) { root.f = 0; buf = arr }
    else              { rc_inc(arr); buf = helper(arr, stride) }

The unique branch stores into the field's own buffer, so the result is its only
name and the field is MOVED out — the same conclusion
`2026-09-04-field-append-in-place.md` reached for the append, for the same
reason: two uncounted names at rc 1 and the first release frees under the other.

The copy branch is native-specific. `__fern_arr_cow_inplace` **decs its receiver
as it copies** ("we're taking the caller's reference"), so a site that does not
inc has given the helper the field's own reference — and under the #4873
caller-side grow bracket, which is what puts the buffer above rc 1 in the first
place, the bracket's post-call dec then frees a buffer the field still names.
Paying the inc inside the else branch balances that and costs the fast path
nothing. The self-host has no such helper — `lower_field_with_inplace` slices
by hand and brackets the ROOT BOX instead — so the two arrive at the same
behaviour by different routes, which is worth remembering before "porting" one
into the other.

### Two refusals the move needs

- **A body-scope host inside a loop**, because the next iteration would read the
  moved-out field. A return leaves the loop and a rebind replaces the root, so
  both keep their sites (the port's rule, `fai_admit_stmt`'s `in_loop`).
- **A root the frame may not hand the buffer away from.** The receiver, a
  parameter (the box is the caller's, and its fields are the grow bracket's job
  — `computeGrowParams` already seeds a field-receiver `.with`), or a local the
  function BUILDS: bound once, at the top level, from a struct literal, and
  assigned nothing else. `var t = o.inner` is a local naming another container's
  box, and emptying its field takes the buffer from a container the analysis
  cannot even see the reads of, since they are rooted at a different name. This
  is the port's #8556 rule; native's append arm still has no root rule at all,
  which is a live interp/native divergence (43 against 44 on its repro), filed
  as **#8768**: 104 of the corpus's 342 in-place field appends have a root the
  port's rule refuses, so it wants its own measurement rather than a one-line
  extension here.

## What each gate holds

The decision cannot be read off an answer — the clone computes the same thing —
so `TestFieldPlaceWithInPlaceShape` counts the runtime uniqueness tests in the
emitted ops (1 for an admitted site, 0 for a refused one) and
`TestFieldPlaceMutationSequencedReads` pins the verdicts. Four new `rcCorpus`
rows hold the runtime halves, each falsified by removing one piece:

| removed | x86-64 result |
|---|---|
| the move-out (null on the unique branch) | `field_with_inplace_local_root_move` exit 134; `field_with_inplace_hash_index` leaks **-640 bytes** (a double free) |
| the copy branch's inc | `field_with_inplace_caller_keeps_container` exit 29 (29 rc underflows), **960 bytes** leaked |
| the loop refusal | `field_with_inplace_refused_shapes` exit -1 |
| the root rule | `field_with_inplace_refused_shapes` exit -1 |
| the sequenced excusal | `pre_len_index` / `with_pre_reads` refuse, and `refset_add` loses its `.with` |

## One more thing the analysis now pays for itself with

`fieldPlaceMutationCopies` builds per-NODE tables, and most functions hold no
field-place mutation at all. A single `hasFieldPlaceMutation` walk in front of
it returns early for those — with the three new tables included, native compiles
`examples/self_host/fern.fern` in 19.7-19.8 s against 20.25-20.33 s before
(4-core container, `-o /dev/null`, interleaved rounds).
