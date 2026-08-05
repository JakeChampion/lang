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

These are not "not yet prioritised" — they are blocked on a vector-type
surface in the IR plus per-backend lowering (SSE2/AVX2, NEON, wasm `v128`).
That is a large, self-contained project, and it is the **prerequisite** for a
whole tier of work rather than a row in it. Where a SIMD algorithm has a
credible SWAR (SIMD-within-a-register, 64-bit-word) variant, the row says so —
SWAR is available today and is often 4–8× over byte-at-a-time.

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
| string → f64 | Clinger fast path + neighbour refinement | Eisel–Lemire + bigint fallback | **GAP — highest-value numeric item** |
| i32/i64 → string | Digit loop with `/ 10` | Lemire multiply-shift, 2-digit table | GAP, easy |
| string → int | Byte-at-a-time accumulate | SWAR 8-digit chunks | GAP, easy |
| Division by constant | Emitted as division | Magic-number reciprocal in the compiler | GAP, compiler-side |

`parse_float` is the natural sibling of the Dragonbox work: same subsystem,
same philosophy, and the current implementation's slow path is a bit-pattern
refinement loop rather than a bounded computation. Eisel–Lemire resolves the
overwhelming majority of real inputs with one 128-bit multiply against a
power-of-ten table, falling back to the existing exact path only on genuine
ties. It needs a `u64 × u64 → u128` high-multiply, which is a small IR
addition (or synthesisable from four 32-bit multiplies).

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
| String hash | FNV-1a 32-bit, byte at a time | wyhash / XXH3 | GAP, easy |
| Scalar hash | Wang mix | Fine | OK |
| Adversarial keys | None — no seeding | SipHash, randomised seed | GAP (matters if Fern serves untrusted input) |
| Tiny map | Full probe machinery | Linear scan below ~8 entries | GAP, easy |
| Checksum | — | xxHash / CRC32 | GAP |

A SwissTable's control-byte metadata and probe *structure* are worth adopting
even without SIMD: a SWAR group probe over a 64-bit word tests 8 slots per
iteration with `(x - 0x0101..) & ~x & 0x8080..`-style tricks. That captures a
good share of the cache-behaviour win, which is where most of SwissTable's
advantage actually comes from.

FNV-1a is a defensible default but is byte-at-a-time and has known weak
avalanche in the low bits — which is exactly where a power-of-two mask reads.
wyhash-style 64-bit block mixing is a contained, well-tested replacement.

The seeding gap is a security consideration, not just a performance one: with
an unseeded, publicly-known hash, an attacker who controls map keys can force
collisions. That matters for the HTTP/JSON surface, less so for the compiler.

### Strings and bytes

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| Substring search | **Two-Way (Crochemore–Perrin)** | Two-Way; SIMD/`memchr` for short needles | **DONE (this pass)** |
| Single-byte search | Scalar scan | SIMD `memchr`; SWAR viable | GAP |
| Backward search (`last_index_of`, `rsplit_once`, `rpartition`) | Naive `O(n·m)` | Reverse Two-Way | GAP |
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
| `count_ones` | **Branchless SWAR** | `POPCNT` / NEON `CNT` / wasm `i32.popcnt` | **DONE (this pass)**; intrinsic still open |
| `leading_zeros` | **Branchless smear + popcount** | `LZCNT` / `CLZ` / `i32.clz` | **DONE (this pass)**; intrinsic still open |
| `trailing_zeros` | **Branchless `x ^ (x-1)` + popcount** | `TZCNT` / `RBIT`+`CLZ` / `i32.ctz` | **DONE (this pass)**; intrinsic still open |
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
| Default RNG | OS CSPRNG (`getrandom`) **per call** | PCG / xoshiro for non-crypto | **GAP** |
| Secure RNG | OS CSPRNG | Correct as-is | OK |
| Bounded integers | **Lemire's debiased method** | Lemire's debiased bounded method | **DONE (this pass)** |
| `std/sim` bounded draw | `x % n` on Park-Miller — **modulo bias** | Debiased mapping + a modern PRNG | GAP (contract-sensitive, see below) |
| Parallel RNG | None | Philox / Threefry | N/A until threads |

`math.random_int`'s modulo bias is fixed (see below). What remains is that it
reaches for the OS CSPRNG on *every* call, so `rand.shuffle` costs one
syscall-backed draw per element — the "don't use one RNG for everything" point
in its most concrete form. A PCG or xoshiro generator behind a separate type
would serve the non-crypto callers (`shuffle`, `sample`, `fuzz`) without
touching the secure path.

**`std/sim` has the same bias, and it is deliberately harder to fix.** Its
`__roll` maps a Park-Miller step with `x % n`, biased for the same reason. But
`sim`'s documented contract is that equal seeds give *bit-identical* runs, so
changing the mapping changes every existing simulation's sequence — a
reproducibility break, not a transparent fix. It also deserves a better
generator than a 1990s LCG. Both are worth doing together, deliberately, with
a note about seed compatibility; neither should be a drive-by.

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
3. **A fast non-crypto PRNG** (PCG or xoshiro) behind a separate type, so
   `shuffle` / `sample` / `fuzz` stop paying a syscall per draw. The
   modulo-bias half of this item is done.
4. **wyhash-style string hash** for `core/map`, plus a small-map linear-scan
   path.
5. ~~Adaptive sort~~, ~~`random_int` modulo bias~~, ~~SWAR bit counting~~ — done, see below.

**Tier 2 — unblocked, narrower**

6. Reverse Two-Way for the backward-search family.
7. SWAR group probing for `Map` (the SwissTable idea, minus the vectors).
8. Lemire integer→string and SWAR string→int.
9. Table-driven lexer classification.
10. Magic-number constant division in the compiler.

**Tier 3 — blocked on a vector surface**

A SIMD surface in the IR (vector types + SSE2/AVX2, NEON, wasm `v128`
lowering) is the single prerequisite that unblocks simdjson-style parsing,
simdutf validation, true SwissTable probing, vectorised `memchr`/`memcmp`, and
sorting networks. It should be evaluated as one project with that whole tier as
its payoff, not attempted piecemeal.

## What landed in this pass

**Substring search is now Two-Way (Crochemore–Perrin).** `std/string`'s search
family was a naive `O(n·m)` scan that re-probed one byte at a time. It is now a
single core, `__str_find_from`, dispatching on needle length: empty → the gap
position, one byte → a `memchr`-shaped scan, longer → Two-Way, which is
`O(n + m)` time and `O(1)` space with no skip table to allocate. `contains`,
`index_of`, `split`, `splitn`, `split_once`, `partition`, `find_all`, `count`,
`count_matches`, `replace`, `replace_n`, and `replacen` all route through it,
which also deleted thirteen hand-written copies of the same probe loop. The
seven remaining `__substr_eq` call sites are the backward searches and the
anchored prefix/suffix strip loops, which are a separate piece of work (see the
backward-search row above).

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

**Bit counting is now branchless SWAR.** `count_ones`, `leading_zeros` and
`trailing_zeros` were 32- and 64-iteration loops in each of std/i32, std/i64,
std/u32 and std/u64. They now accumulate the count in-place in 2-, then 4-,
then 8-bit fields of the word; leading zeros smears the top set bit downward
and subtracts a popcount; trailing zeros uses the `x ^ (x - 1)` identity. All
branchless, so the cost no longer depends on the value. Measured 4.3x faster on
x86-64 over 3M iterations of all three.

The per-byte totals are summed by folding the word onto itself rather than by
the customary `* 0x01010101 >> 24` multiply, which sidesteps any question about
how unsigned multiplication overflows in this language; and the 64-bit masks
are built by doubling the 32-bit constants rather than written as literals.
Verified against the loop implementations they replaced, recomputed inside the
test program: every `1<<k` and its neighbours at both widths, 3000
pseudo-random values including negatives, and the zero / all-ones boundaries
where smear-and-count and the `x ^ (x-1)` identity are least obviously correct.

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
