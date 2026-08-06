# SOTA standard-library blueprint

A survey of where Fern's stdlib sits against the best known algorithm for each
primitive, and what it would take to close each gap. This is a **research and
prioritisation document**, not a plan of record: every row records what the
code does *today* (verified by reading it, not assumed), what the literature's
current best is, and a verdict.

The organising philosophy — the one worth keeping even where the individual
rows go stale:

> A mathematically strong general algorithm, with specialised fast paths
> around the common cases and an exact/robust fallback for the pathological
> ones.

Dragonbox and Eisel–Lemire are the canonical examples, and `parse_float`'s
shape (Clinger fast path → refinement) is the same idea. The mistake this
document exists to prevent is picking one clever algorithm and applying it
everywhere, instead of dispatching on type, size, and data shape.

## Three constraints that reorder the generic advice

Most "SOTA stdlib" shortlists are written for C++ or Rust on x86-64. Three
facts about Fern change the ranking substantially, and every verdict below is
conditioned on them.

**1. There is no SIMD — anywhere.** Not in `internal/ir`, not in any of the
three backends, not as an intrinsic surface in the language. This is the
single most important constraint, because it removes the top item from most
published shortlists:

| Technique | Status in Fern |
| --- | --- |
| simdjson-style stage 1/2 parsing | Not implementable |
| simdutf-style block UTF-8 validation | Not implementable |
| SwissTable SIMD group probing | Not implementable as designed |
| SIMD `memchr` / `memcmp` | Not implementable |
| Sorting networks over vector registers | Not implementable |

These are not "not yet prioritised" — they need per-backend lowering
(SSE2/SSE4.2, NEON, wasm `v128`), which is the **prerequisite** for a whole
tier of work rather than a row in it. Where a SIMD algorithm has a credible
SWAR (SIMD-within-a-register, 64-bit-word) variant, the row says so — SWAR is
available today and is often 4–8× over byte-at-a-time.

**Amended (2026-08-06):** the rows above originally read as blocked on a
*vector-type surface in the IR*, which turned out to overstate the
prerequisite — `docs/ATLAS-PLATFORM-PLAN.md` §1.2 argues the payoff is
reachable without one. "Not implementable" in the table above should be read as
"not implementable as the published algorithm is written", not as "unreachable
in Fern". The declared CPU baselines already guarantee 128-bit SIMD on all
three targets, so the tier also needs no CPU feature detection or runtime
dispatch (§1.1).

**2. Memory is a bump arena plus reference counting, not a general malloc.**
The mimalloc / jemalloc / snmalloc / tcmalloc branch of the usual shortlist
mostly does not apply: Fern already has the "several allocation mechanisms"
answer the advice is pushing toward (16 GiB `MAP_NORESERVE` bump arena, a
large-tier freelist, Perceus RC with constructor reuse). The open questions
here are about *reuse and over-retention*, not about allocator selection.

**3. The compiler is the biggest workload.** The self-hosted compiler is a
long-running, allocation-heavy Fern program, and it is the most demanding
consumer of `std/string` and `core/map` in existence. A string or map
improvement is not merely a library win — it compounds into compile times.
This is why substring search ranked first in this pass.

A fourth, smaller one: `.index_of` / `.contains` / `.split` / `.starts_with` /
`.ends_with` on a `string` receiver are **compiler builtins** in the self-host
compiler, lowered to emitted `__fern_str_*` runtime helpers. When a program
imports `std/string`, method dispatch resolves to the stdlib function (verified
by inspecting emitted wat: the Two-Way core is present, the builtin helper is
not) — but `split` still lowers to its helper. Any change to a stdlib string
primitive should check whether a sibling helper exists in
`examples/self_host/asmcore.fern` (`rt_src_str_*`, used by the native backends)
or `examples/self_host/wasm_ir.fern` (`*_helper`, hand-written WAT), or the two
paths will silently diverge. They did, for `split("")`; see below.

## Status table

Verdicts: **DONE** shipped · **GAP** worth doing, unblocked · **BLOCKED** needs
SIMD or another prerequisite · **N/A** doesn't apply to Fern's model · **OK**
current implementation is already the right answer.

### Numeric conversion

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| f64 → shortest string | Dragonbox (#6161) | Dragonbox / Schubfach | **DONE** |
| f64 → fixed/scientific | `to_string_prec` | Ryū Printf | GAP, low value |
| string → f64 | **Eisel–Lemire + exact fallback** (#6167) | Eisel–Lemire + bigint fallback | **DONE** (landed on main independently) |
| i32/i64 → string | **2-digit table, exact-size buffer** | Lemire multiply-shift, 2-digit table | **DONE (this pass)** (magic-number division still open) |
| string → int | Byte-at-a-time accumulate | SWAR 8-digit chunks | GAP — **blocked on the same raw-load question as wyhash (#6200)** |
| Division by constant | Emitted as division | Magic-number reciprocal in the compiler | GAP, compiler-side |

`parse_float` **is done** — Eisel-Lemire landed on main in #6167 (~2,400x)
while this branch was in flight, with the table generated by
`cmd/floattablegen` and packed into a string literal, exactly the idiom
`std/unicode` uses for its case tables.

This row read "GAP — highest-value numeric item" for most of this pass, and
that was accurate against the code the branch was cut from; it is recorded
here as a caution about the document's shelf life rather than quietly edited
away. An audit of a moving codebase is a photograph, and the right habit
before starting any row is to re-check it against current `main` rather than
against this file.

### Sorting

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| `cmp.sort` / `sort_desc` | **Adaptive natural-run merge sort** | pdqsort (unstable) / Timsort (stable) | **DONE (this pass)** |
| Small-N sort | Insertion sort below 32 | Insertion sort below ~24 | **DONE (this pass)** |
| Presorted input | Natural run detection, O(n) | Natural run detection (Timsort) | **DONE (this pass)** |
| MIN_RUN extension | Not done — blocked, see below | Timsort's short-run extension | BLOCKED (self-host IR gap) |
| Galloping merge | None | Timsort's galloping mode | GAP |
| Integer sort | Comparison sort | Radix / American flag | GAP |
| `sort_by` | Merge sort over a comparator | Same adaptive treatment as `cmp.sort` | GAP |
| Unstable sort | None — `sort` is the only option | pdqsort | GAP |
| Tiny fixed-size sort | None | Sorting networks | BLOCKED (wants SIMD to pay off) |

`sort_by` (in `std/sort`) still has the old fixed bottom-up shape and deserves
the same treatment; it was left alone here to keep the change reviewable.

### Hash tables and hashing

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| `Map` layout | Open addressing, separate key/value columns | SwissTable control bytes + group probe | BLOCKED on SIMD; **SWAR variant viable** |
| String hash | **FNV-1a over 4-byte blocks + fmix32 avalanche** | wyhash / XXH3 | **DONE (this pass)** |
| Scalar hash | Wang mix | Fine | OK |
| Adversarial keys | **Per-process seed XORed into the FNV basis, default on** | SipHash, randomised seed | **DONE for offline attacks (this pass)**; SipHash still wanted against an online oracle |
| Tiny map | **Linear scan at or below 8 entries** | Linear scan below ~8 entries | **DONE (this pass)** |
| Checksum | — | xxHash / CRC32 | GAP |

A SwissTable's control-byte metadata and probe *structure* are worth adopting
even without SIMD: a SWAR group probe over a 64-bit word tests 8 slots per
iteration with `(x - 0x0101..) & ~x & 0x8080..`-style tricks. That captures a
good share of the cache-behaviour win, which is where most of SwissTable's
advantage actually comes from.

The string hash now mixes 4-byte blocks and finishes with an fmix32
avalanche (see below). Going further — wyhash/XXH3-style 64-bit block mixing —
needs a raw pointer into the string and unaligned 8-byte loads to be worth it;
assembling a 64-bit word from eight indexed byte loads costs more than it
saves. That makes it a bigger change than it looks, gated on how comfortable
the language is exposing string data pointers.

Seeding is a security consideration, not just a performance one: with an
unseeded, publicly-known hash, an attacker who controls map keys can force
collisions. That matters for the HTTP/JSON surface, less so for the compiler.

String-keyed maps now carry a **per-process seed** (#6194), drawn once from the
same CSPRNG as `random_i32` via the compiler-internal `__map_hash_seed()` and
cached in a runtime word, then XORed into the FNV offset basis. It is on by
default: an opt-in seed would have to be threaded through map literals,
`map.from` and `json.parse` to reach the keys that actually need it, which is
exactly the surface an attacker picks. Enabling it by default is safe because
bucket order is not observable — `core/map` iterates the insertion-ordered
entry array, never the bucket table, so `std/json`'s key-order preservation is
unaffected.

What that closes and what it does not: the seed defeats OFFLINE precomputation,
which is the whole of the practical hash-flooding attack, because the colliding
set has to be computed against a seed drawn per process. It does NOT defeat an
attacker with an online timing oracle — FNV is invertible, so enough per-request
feedback recovers the seed. SipHash-1-3 behind the same seed slot is the
upgrade, and it is a drop-in: `__map_fnv_str_seeded` is the only consumer of the
seed, so nothing outside it depends on how the seed is used.

### Strings and bytes

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| Substring search | **Two-Way (Crochemore–Perrin)** | Two-Way; SIMD/`memchr` for short needles | **DONE (this pass)** |
| Single-byte search | Scalar scan | SIMD `memchr`; SWAR viable | GAP |
| Backward search (`last_index_of`, `rsplit_once`, `rpartition`) | **Metered naive scan escalating to reverse Two-Way** | Reverse Two-Way | **DONE (this pass)** |
| `split` / `replace` / `find_all` / `count` | Routed through the Two-Way core | — | **DONE (this pass)** |
| Case-insensitive search | Naive | Case-folded Two-Way | GAP |
| `memcpy` / `memcmp` | Runtime helpers | Vectorised, alignment-aware | BLOCKED (SIMD) / SWAR viable |

### Unicode

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| UTF-8 validation | Byte-at-a-time DFA | simdutf block validation | BLOCKED (SIMD); **DFA is already the right scalar answer** |
| UTF-8 length / decode | Scalar | Scalar is fine below SIMD | OK |
| UTF-8 ↔ UTF-16 | `std/utf8` | simdutf | BLOCKED |

### Parsing

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| JSON | `std/json`, scalar | simdjson stage 1/2 | BLOCKED (SIMD) |
| Lexer character classes | Branch chains in places | 256-entry lookup tables | **GAP, easy, applies to the compiler's own lexer** |
| CSV | Scalar | SIMD quote/delimiter scan | BLOCKED |
| Number parsing in parsers | Shared with `parse_float` | Eisel–Lemire | GAP (see above) |

Table-driven character classification is the part of the simdjson lesson that
survives without SIMD, and the compiler's own lexer is the beneficiary.

### Bit manipulation

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| `count_ones` | **Intrinsic** (`i32.popcnt` on wasm; inline SWAR on the register backends) | `POPCNT` / NEON `CNT` / wasm `i32.popcnt` | **DONE**; hardware popcount needs a baseline decision |
| `leading_zeros` | **Intrinsic** — `i32.clz` / arm64 `clz` / x86 `bsr` | `LZCNT` / `CLZ` / `i32.clz` | **DONE (this pass)** |
| `trailing_zeros` | **Intrinsic** — `i32.ctz` / clz-derived / x86 `bsf` | `TZCNT` / `RBIT`+`CLZ` / `i32.ctz` | **DONE (this pass)** |
| `byte_swap` | Software | `BSWAP` / `REV` | GAP |
| `rotate_left/right` | Software | `ROL`/`ROR` / wasm `rotl` | GAP |

These were 32- and 64-iteration software loops in all four integer modules —
the source comment gave the reason plainly: "no intrinsics surface in lang".
They are now branchless SWAR, which needs no compiler work at all and is
**measured 4.3x faster** on x86-64 (3M iterations of all three ops: 1250-1282 ms
before, 278-299 ms after; the loop overhead is inside both figures, so the
speedup on the bit ops alone is larger).

The hardware intrinsics are still the endpoint and still worth doing — a single
instruction beats eight or ten. But that needs a new IR op family plus lowerings
in three native backends AND three self-host backends to avoid a parity gap, so
it is a compiler project, not the one-line change this row used to imply. The
SWAR versions capture most of the win in the meantime, and anything built on
them (hash mixing, bit-set iteration, `bit_length`, a future SWAR layer
elsewhere) gets it for free.

### Random numbers

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| Default RNG | OS CSPRNG per call; **seeded PCG32 alongside** | PCG / xoshiro for non-crypto | **DONE (this pass)** |
| Secure RNG | OS CSPRNG | Correct as-is | OK |
| Bounded integers | **Lemire's debiased method** | Lemire's debiased bounded method | **DONE (this pass)** |
| `std/sim` bounded draw | **Lemire on PCG32**, via `std/rand` | Debiased mapping + a modern PRNG | **DONE (this pass)** |
| Parallel RNG | None | Philox / Threefry | N/A until threads |

`math.random_int`'s modulo bias is fixed, and `std/rand` now carries a seeded
PCG32 alongside the CSPRNG path (both below). The CSPRNG functions are
unchanged and remain the default, so nothing silently became less secure; the
`*_seeded` siblings are opt-in and ~4-5x faster in bulk.

`std/fuzz` now draws from the seeded generator, so a fuzz run is reproducible
from its seed (below).

**`std/sim` had the same bias, and fixing it cost a compatibility break.** Its
`__roll` mapped a Park-Miller step with `x % n`. Debiasing the mapping alone
would have spent the break without buying the quality — Park-Miller is a 1990s
LCG whose low bits cycle with short periods, and a small-`n` modulo reads
exactly those bits — so both moved at once: `__roll` now takes one PCG32 step
from `std/rand` and maps it with Lemire rejection (#6193).

The cost is stated plainly rather than hidden: **a seed recorded before the
change replays a different sequence.** Equal seeds still give bit-identical
runs — that contract is untouched, and it is what `sim` exists to provide —
but *which* run a given seed produces moved. Anything pinned to an old seed
(a saved DST replay, a regression named by seed) has to be re-recorded; the
in-tree golden tests were, rather than relaxed.

### Big integers, math, and the rest

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| Bigint multiply | No bigint type | Schoolbook → Karatsuba → Toom-Cook → NTT | N/A (no arbitrary-precision type) |
| Transcendentals | libm on natives; polynomial approximations on wasm | RLIBM correctly-rounded | GAP, with an accuracy-contract decision attached |
| Date/time | `std/time` | Howard Hinnant's civil-date algorithms | Worth an audit |
| Compression | None | LZ4 / Zstd | N/A (not in the library) |
| Crypto | `std/crypto` | BLAKE3, hardware AES/SHA | GAP (hardware acceleration needs an intrinsic surface) |
| Parallel algorithms | None | Work stealing, parallel scan | N/A until a threading model exists |

The transcendental row carries a design question, not just an implementation
one: the wasm path uses polynomial approximations while the native paths call
libm, so `sin(x)` can differ across backends **today**. Fern should decide
whether it promises correctly-rounded results, a stated ULP bound, or merely
"whatever the platform does" — and if it promises anything, the wasm path is
where the promise breaks first. The `fast_sin` / `sin` API split the essay
suggests is one way to make that explicit.

## Recommended order

Ranked by (value to real workloads) ÷ (implementation risk), given the
constraints above.

**Tier 1 — unblocked, high value**

1. **Hardware bit intrinsics.** An IR op family plus lowerings in three native
   and three self-host backends — a compiler project, not a one-liner. The SWAR
   implementations now in the stdlib capture most of the win, so this is no
   longer urgent.
2. **Eisel–Lemire `parse_float`.** The sibling of the Dragonbox work; needs a
   128-bit high-multiply.
3. ~~Move `std/fuzz` onto the seeded generator~~ — done, see below.
4. ~~Small-map linear-scan path~~, ~~per-process hash seed~~ — done, see
   below. What remains on `core/map` is the SWAR group probe, and SipHash for
   the seeded path (the seed closes offline precomputation; an online timing
   oracle can still recover an invertible FNV).
5. ~~Adaptive sort~~, ~~`random_int` modulo bias~~, ~~SWAR bit counting~~,
   ~~bit-counting intrinsics~~,
   ~~map string hash~~, ~~seeded PCG32~~ — done, see below.

**Tier 2 — unblocked, narrower**

6. ~~Reverse Two-Way for the backward-search family~~ — done, see below.
7. SWAR group probing for `Map` (the SwissTable idea, minus the vectors).
8. ~~2-digit integer→string~~ — done, see below. SWAR string→int is blocked with #6200.
9. Table-driven lexer classification.
10. Magic-number constant division in the compiler.

**Tier 3 — blocked on a vector surface**

A SIMD surface in the IR (vector types + SSE2/AVX2, NEON, wasm `v128`
lowering) is the single prerequisite that unblocks simdjson-style parsing,
simdutf validation, true SwissTable probing, vectorised `memchr`/`memcmp`, and
sorting networks. It should be evaluated as one project with that whole tier as
its payoff, not attempted piecemeal.

**That evaluation has since happened — see `docs/ATLAS-PLATFORM-PLAN.md` §1.2
and §3.** Its conclusion changes this tier's cost, not its payoff: a
first-class vector *type* needs a second register class in six backends
(both native backends are stack machines over 8-byte operand slots, and f64
already proves the pattern — the vector register file is entered and left
inside a single op), whereas every item on the payoff list above is a
whole-loop kernel with scalar inputs and a scalar result. Confining the vector
lifetime inside one IR op reaches the same payoff with no register class, no
regalloc change, and no type-system change. The tier is therefore **no longer
blocked on a language-level vector type**; it is sequenced behind a
performance-regression gate, with `__memchr` as the first kernel.

## What landed in this pass

**Substring search is now Two-Way (Crochemore–Perrin).** `std/string`'s search
family was a naive `O(n·m)` scan that re-probed one byte at a time. It is now a
single core, `__str_find_from`, dispatching on needle length: empty → the gap
position, one byte → a `memchr`-shaped scan, longer → Two-Way, which is
`O(n + m)` time and `O(1)` space with no skip table to allocate. `contains`,
`index_of`, `split`, `splitn`, `split_once`, `partition`, `find_all`, `count`,
`count_matches`, `replace`, `replace_n`, and `replacen` all route through it,
which also deleted thirteen hand-written copies of the same probe loop. The
remaining `__substr_eq` call sites are the anchored prefix/suffix strip loops,
which are correctly `O(m)` per iteration and want no search algorithm at all.

**Integer→string takes two digits per division.** `core/int`'s `int_to_string`
/ `__int_to_string_u64` were one divide AND one modulo per decimal digit,
written backwards into an over-sized scratch buffer and then COPIED into a
right-sized one — ten divisions, ten modulos, and two allocations for a
ten-digit number. They now index a 200-byte digit-pair table two digits at a
time, and compute the width up front (`__int_u32_digits`, a comparison ladder)
so the result is written straight into an exactly-sized buffer with nothing to
re-pack. Five divisions and one allocation for the same number. Measured on 4M
i32 conversions, x86-64: **0.721s → 0.463s**.

That is short of the SOTA entry, which is Lemire's multiply-shift: replace the
division entirely with a fixed-point reciprocal multiply. The obstacle is not
in the stdlib — none of the backends turn a division by a compile-time constant
into the multiply-and-shift a C compiler emits, so writing the reciprocal by
hand in Fern means writing a 64-bit multiply-high the backends would then lower
badly. That is the magic-number-division row further down, and it is where the
remaining factor lives.

**`parse_int` is untouched, deliberately.** Its SOTA form is SWAR: load eight
digit bytes as one 64-bit word and fold them with three multiplies. That needs
an unaligned 8-byte load out of a string, which the language has no way to
express — exactly the blocker on wyhash (#6200). Halving the loop to two digits
at a time was possible, but on the 1-10 digit strings `parse_int` actually sees
it trades a real increase in branching for a handful of iterations, so it was
left alone rather than half-done.

**Backward search is linear too, by escalation rather than replacement.**
`last_index_of` / `rfind` / `rsplit_once` / `rpartition` shared a naive
right-to-left probe loop, `O(n·m)` worst case. They now share
`__str_rfind_from`, which dispatches like its forward sibling — the gap for an
empty needle, a backward `memchr`-shaped scan for one byte — and for anything
longer runs the naive scan under a LINEAR COMPARISON BUDGET, escalating to the
reverse Two-Way (reverse both strings, run the forward algorithm, map the index
back) once the budget is spent.

The budget is the whole design, and it exists because reverse Two-Way is not a
free upgrade the way the forward one was. Forward Two-Way is `O(1)` space;
reverse Two-Way needs an `O(n)` copy of the haystack to run the forward
algorithm over. Reversing a megabyte to find a two-byte separator the naive
scan would have hit twenty bytes in is a pessimisation. Under the budget the
common case (a short separator near the end, which is what `rsplit_once` is
usually asked for) keeps its `O(1)` space and never allocates, while the
quadratic case gets its linear bound. Measured on the adversarial shape — 40 KB
of `a`, needle 2000×`a`+`b` — **2.655s → 0.014s**.

The honest framing: on ordinary text where the first byte rarely matches, the
naive scan was already about `n` comparisons and Two-Way is comparable. The win
is the **guarantee** — repetitive input (the compiler's own source, JSON, log
lines, DNA-like data) is where the old code degraded quadratically, and that
degradation is now gone.

Verified by differential testing rather than examples: a harness enumerates
every haystack over `{a,b}` up to length 8 and every needle up to length 3,
checking each of the twelve functions against a naive reference computed in the
same program — 7,154 cases per backend, plus 51,396 cases for the search core
over a wider enumeration, run on interp, x86-64, wasm, and through the
self-host compiler on both wasm and x86-64. A binary alphabet is what actually
exercises Two-Way's periodic branch; fixed example strings mostly miss it.

**`cmp.sort` / `cmp.sort_desc` are now an adaptive natural-run merge sort.**
The previous implementation materialised a fresh full copy of the array on
every one of its `ceil(log2 n)` merge passes, with no insertion-sort base case
and no run detection — so an already-sorted array cost exactly as much as a
random one. Now: `n <= 32` is a single insertion sort; larger inputs have their
natural runs detected (descending runs found with a strict compare and reversed
in place, which keeps the sort stable) and then merged in balanced rounds.
Sorted and reverse-sorted inputs are a single run, so they cost O(n) with one
copy; the worst case stays O(n log n).

Verified against a naive selection sort across every length from 0 to 80 —
straddling the size-32 threshold and the merge rounds — with only 7 distinct
values so ties are dense, plus the sorted / reverse / all-equal / sorted-then-
descending shapes, on interp / x86-64 / wasm and through the self-host compiler
on wasm and x86-64. Stability is checked separately with a key-only `Ord` impl
and a tag witnessing input order, over 100 elements and 5 keys so equal-key
groups span several runs and survive multiple merges. That stability test is
**mutation-checked**: flipping the merge's tie-break to take from the right run
makes it fail with a tag inversion, so it is known to have teeth.

**A self-host IR-subset gap was found and worked around (not fixed).** Calling
a free **generic** function from inside a loop body — or from a short-circuit
operand of a loop condition — makes the self-host IR path bail on a
monomorphised generic caller. Since the AST emitters were deleted, a bail is a
hard compile error, so this is not a performance footnote: the first draft of
this sort simply would not compile for the self-host backends. Bisected to a
minimal shape: a generic function that calls a generic helper inside a `while`
bails, while the same call outside the loop, a non-generic call in the same
position, and a trait-method call (`x.cmp(y)`) inside the loop all lower fine.

The workaround is to use inline `.cmp()` everywhere and negate the three-way
result for descending order, so no free generic helper is called from a loop.
The visible cost is Timsort's MIN_RUN step: extending short runs by insertion
sort would mean calling the insertion helper from inside the run loop, so it is
omitted, which leaves a deeper merge tree on random input. Both constraints are
documented at the call site so a later tidy-up does not silently reintroduce
the bail. **This gap is worth fixing at the compiler level** — it is a real
constraint on how stdlib generics can be written, and it is invisible until
something bails.

**`random_int` is now unbiased.** It mapped a 32-bit draw with a bare
`u % range`, which is biased whenever `range` does not divide 2^32: the first
`2^32 mod range` values come up one extra time per 2^32 draws. `rand.shuffle`
is Fisher-Yates over `random_int`, so shuffles were not uniform permutations.
It now uses Lemire's nearly-divisionless method — multiply-shift for the
mapping, rejecting the surplus zone the product's low half identifies. In the
common case that costs one comparison and no division at all, so it is cheaper
than the `%` it replaces as well as correct. The old code's comment already
listed this as a known follow-up.

Testing this honestly needs two programs, because neither half is sufficient
alone. The behavioural one exercises the real `random_int` — containment across
range widths, every bucket reachable, degenerate and boundary ranges, and
termination on the widest possible range — but it **cannot observe the bias**:
the residual is ~2^-32 and no feasible sample size would reveal it. So a second
program runs the identical multiply-shift-and-reject formula at an 8-bit word
size, enumerates all 256 draws exhaustively, and asserts perfect uniformity
with exactly `W mod range` rejections. It uses the same u64 multiply / shift /
mask as the implementation, so it also pins that arithmetic on each backend —
the part most likely to differ between interp, x86-64, wasm and arm64. It
additionally computes the OLD biased mapping and asserts it is measurably
skewed (86/85/85 for range 3), so the suite cannot silently pass if the fix is
reverted.

**Bit counting goes through compiler intrinsics.** `count_ones`,
`leading_zeros` and `trailing_zeros` on `std/{i32,i64,u32,u64}` were branchless
SWAR sequences — 12-15 ALU ops each, portable and correct, and the right answer
while the language had no intrinsic surface. They are now one-line wrappers over
`__popcount*` / `__clz*` / `__ctz*`, each lowering to a SINGLE IR op
(`OpPopcount` / `OpClz` / `OpCtz`) across all six backends plus the interpreter.

**Measured, because the estimate mattered.** On x86-64, 20M `count_ones()`
calls: **0.489s → 0.127s (3.9x)**. `leading_zeros` lands at 0.108s against a
0.099s bare-loop floor — effectively free. The isolation is what justified the
work: the same loop with a trivial callee costs 0.099s, so the SWAR *body* was
~80% of the runtime and call overhead was negligible. A first reading off C
timings alone suggested the win would be under 10%; it was wrong, and only
measuring the three variants separately showed why.

**What each backend actually emits differs, deliberately:**

| | clz | ctz | popcount |
| --- | --- | --- | --- |
| wasm | `i32.clz` | `i32.ctz` | `i32.popcnt` |
| arm64 | `clz` | `x & -x` then `clz` + `csel` | inline SWAR |
| x86-64 | `bsr` + zero branch | `bsf` + zero branch | inline SWAR |

The two popcount gaps are **not** oversights. On x86-64, `POPCNT` requires
SSE4.2 and Fern emits static binaries with no runtime CPU dispatch, so using it
would turn a pre-2008 CPU into a SIGILL at the first bit operation rather than
a slow binary — raising the baseline is a project decision, not a codegen one.
On arm64 the hardware popcount lives on the SIMD side (`cnt` per byte, `addv`
to sum), and neither `cnt`, `addv`, nor `rbit` is implemented by the in-process
assembler `cmd/fern -target arm64` uses by default; emitting them fails at
assemble time. Both still gain from being inline on the IR path rather than
behind a Fern-level call, which is where the 3.9x comes from.

Zero is defined: clz/ctz of 0 return the operand width, matching wasm's
semantics and what the SWAR code returned.

The per-byte totals are summed by folding the word onto itself rather than by
the customary `* 0x01010101 >> 24` multiply, which sidesteps any question about
how unsigned multiplication overflows in this language; and the 64-bit masks
are built by doubling the 32-bit constants rather than written as literals.
Verified against the loop implementations they replaced, recomputed inside the
test program: every `1<<k` and its neighbours at both widths, 3000
pseudo-random values including negatives, and the zero / all-ones boundaries
where smear-and-count and the `x ^ (x-1)` identity are least obviously correct.

**`core/map`'s string hash mixes blocks and avalanches.** It was textbook
byte-at-a-time FNV-1a. It now folds 4 bytes per multiply and finishes with a
murmur3 fmix32, because callers mask the hash with `cap - 1` and so read only
the LOW bits — where FNV-1a diffuses worst, since its multiply pushes influence
upward while a trailing byte's xor only perturbs the bottom 8.

The numbers are worth stating plainly, because the intuition oversells this.
Bucket occupancy moves close to ideal: 1000 short keys into 1024 buckets fill
618 of them versus 562 before, against ~638 for a uniform hash; 512
common-prefix keys into 1024 fill 402 versus 392, against ~403. End to end that
is about **3-5%** on a map-lookup-heavy benchmark (4000 identifier-shaped keys,
60 lookup passes: 58-60 ms before, 56-58 ms after) — hashing is only one part
of a lookup alongside probing and key comparison. It is **not** a 4x win
despite the per-block mix being 4x cheaper than the per-byte one: the fixed
fmix32 tail eats much of that back, and for 1-3 byte keys it is a net increase
in multiplies, bought for the better spread.

Changing this hash cannot change map semantics — iteration is insertion order
off an entry array, not bucket order, and lookups re-check keys by equality —
so the risk was distribution and probing, not correctness. Covered by a
public-API round trip across key lengths chosen around the 4-byte block
boundary (both loops), a 2000-key insert/lookup/delete cycle, and an occupancy
comparison that the OLD hash fails, so removing the avalanche breaks the
suite. The self-host compiler was also rebuilt against the new hash — it
leans on maps for its symbol tables — and still compiles every verification
program in this pass correctly.

**`std/rand` gained a seeded PCG32 generator.** `shuffle` / `choice` /
`sample` draw from the OS CSPRNG — one syscall per draw. That is the right
default and is unchanged, but it costs: shuffling an n-element array makes n
syscalls. The `*_seeded` siblings run in userspace and measured **~4-5x
faster** (20 shuffles of a 2000-element array: 30-35 ms through the CSPRNG,
7 ms seeded), and a seed reproduces a run exactly, which is what simulations,
fuzz corpora and test-data generation actually want.

PCG32 over xoshiro for its state: 64 bits in one word, seeded from a single
i64 with no separate splitmix step, and no all-zero state to special-case.
The generator is pinned to PCG32's real output — the first six u32s for seed
42 are checked against values computed independently from the algorithm
definition, which catches a wrong multiplier or a mis-ordered xorshift that a
distribution test would wave through.

**A language gap forced the API shape, and it is worth knowing about.** The
natural design is a handle with interior mutability — `Rng { state: Cell[i64] }`
— and that is what this was written as first. **`Cell` does not lower on the
self-host compiler's IR path**: a two-line `Cell[i64]` program bails, and since
the AST emitters were deleted a bail is a hard compile error, not a fallback.
So the state is threaded explicitly instead (`rng_next(state) -> (state',
value)`), with `*_seeded` wrappers hiding it for the common case. That is
arguably the more honest shape for a value-semantic language anyway.

The same gap means **`std/sim` is currently uncompilable by the self-host
compiler** — it is built on `Cell`, and `sim____roll` bails. That is
pre-existing and unrelated to this pass, but it is a real hole: a stdlib
module that only works under the interpreter and the native backends. Either
`Cell` should lower on the IR path or `std/sim` should thread its PRNG state
the way `std/rand` now does.

**`std/fuzz` mutations come from the seeded generator, and failures are now
replayable.** Mutation drew from `math.random_int` at 14 sites — a syscall per
draw, up to two per mutation, and no way to reproduce a run. It now threads a
PCG32 state; `fuzz_run` draws ONE seed from the CSPRNG, names it in the failure
diagnostic, and `fuzz_run_seeded(seeds, iterations, rng_seed, target)` replays
that exact sequence. A fuzz failure you can re-run is worth considerably more
than one you can only read about.

**It also surfaced a self-host x86-64 miscompile, and the workaround is
documented at the call site.** The obvious shape for `fuzz_run` is a one-line
delegation to `fuzz_run_seeded` with a freshly drawn seed. That delegation
makes the callee invoke a DIFFERENT function value than the one passed:
`fuzz_run` with an always-passing target reported a failure raised by a target
from an earlier call. Established by bisection — the pre-delegation shape and
the inlined shape both behave, tail and non-tail delegation both misbehave.

The trigger is narrower than "forwarding a function parameter", which is why
`fuzz_run` is inlined rather than this being filed as a blanket hazard: a
single-module forward of an i32-returning fn, of an enum-returning fn, and
**std/array's own `(xs: T[]) any(pred) { return any(xs, pred); }` receiver
delegations** all lower correctly — that last one checked specifically,
because if it had failed the stdlib would be silently miscompiling today. It
does not. Something about this particular shape does; isolating it further is
compiler work, not stdlib work.

**A third gap, found the same way: `std/test` cannot run on the self-host wasm
path.** A bare `import "std/test"` program compiles but wasmtime rejects the
module at `test__assert_at_f32` — the f32/f64 emit gap that also makes
`TestArrayStatsWasm` / `TestArrayMedianRangeWasm` skip. That matters more than
it looks: `std/test` is the pure-Fern runner the project plans to migrate to
once Go-side tests retire.

**Small maps skip hashing and scan.** At or below 8 entries `core/map` now
walks the entry array directly instead of hashing and probing. This is sound
rather than a heuristic shortcut: delete is swap-with-last, so entries
`[0, len)` are all live with no tombstones, and a scan returns the same entry
index a probe would.

Measured before implementing, because the direction was not obvious — a scan
does up to 8 key comparisons where a probe does one hash and usually one. On a
6-entry string-keyed map over 1.2M lookups the standalone comparison was 213 ms
hash-and-probe versus 96 ms scanning, and wiring it into the map moved the same
benchmark from 213 ms to 165 ms (~22%; the rest is call overhead and bounds
checks the microbenchmark does not pay). The standalone figure uses query
strings built separately from the stored keys, so it is not an artefact of a
pointer-equality fast path in `eq_fn`.

The threshold is deliberately modest. The scan is O(len) per lookup, so it must
not extend to sizes where the probe's O(1) wins; eight caps it at eight
comparisons, which is about where a table is still dominated by the fixed cost
of hashing. Covered by sizes 0-12 straddling the threshold, each deleted back
down to empty with survivors re-checked at every step — the case that would
break if the no-tombstone assumption above were ever violated.

**A native/self-host divergence in `split("")` was found and fixed.**
`std/string.split` char-splits an empty separator, but the self-host compiler
lowers `.split()` to its own runtime helper, and both copies of that helper —
`rt_src_str_split` in `asmcore.fern` (native backends) and `str_split_helper`
in `wasm_ir.fern` (hand-written WAT) — returned `[s]` instead. So
`"abc".split("")` was `["a","b","c"]` natively and `["abc"]` self-host-compiled.
The comment recorded it as deliberate: it matched the hand-written asm emitter,
and no differential test covered empty separators. Both halves of that
rationale had expired — the hand-asm emitters were deleted, and the differential
test now exists on every backend.
