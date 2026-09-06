# SOTA standard-library blueprint

A survey of where Fern's stdlib sits against the best known algorithm for each
primitive, and what it would take to close each gap. This is a **research and
prioritisation document**, not a plan of record: every row records what the
code does *today* (verified by reading it, not assumed), what the literature's
current best is, and a verdict.

Its companion is **`SOTA-STDLIB-MATRIX-2026.md`**, which turns this audit into
an execution matrix: a prerequisite, a phase and an acceptance gate per row,
plus an explicit verdict for every entry on the standard published shortlist —
including the ones Fern should not adopt. Read this file for *where we stand*
and that one for *what to pick up next*. When a row lands, update both: the
verdict there, the measurement here.

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

**1. SIMD exists now, and the prerequisite has been paid.** This section used
to open "There is no SIMD — anywhere", and that was the audit's single most
important finding. It is no longer true, and the change is what reorders
everything below it.

Four fused kernels have shipped through `docs/ATLAS-PLATFORM-PLAN.md` §3's
contract — a kernel is one IR op taking scalars and returning a scalar, with
its whole vector lifetime inside its own emitted sequence, so it needs no
vector register class, no regalloc change and no ABI change:

| Kernel | State |
| --- | --- |
| `__memchr` | Vector on all **eight** backends; `std/string`'s single-byte search routes through it (~43x) |
| `__ascii_run` | Vector on all eight; `std/utf8`'s `is_valid_utf8` routes through it (0.22 → 13.8 GB/s) |
| `__rmemchr` | Vector on all eight (8.8x native x86-64); `last_index_of` routes through it |
| `__count_byte` | Vector on all eight (10.2x native x86-64); `std/string`'s `count_byte` IS the kernel — the first TOTAL adoption rather than a needle-length tier |

Eight, not seven: both `-backend ssa` legs carry the kernels, and the x86-64 one
went uncounted through three of the four builds. `docs/ATLAS-PLATFORM-PLAN.md`
§3.4 records what that cost and why no gate saw it.

So the rows below no longer split into "implementable" and "not". They split
three ways, and the distinction matters because two of them are cheap and one
is a decision:

| Technique | Status in Fern |
| --- | --- |
| SIMD `memchr` | **DONE**, forward and backward |
| SIMD byte counting | **DONE** — `__count_byte`, and `wc -l` / `wc -c` route through it |
| simdutf-style block UTF-8 validation | **PARTLY DONE** — `__ascii_run` is the ASCII-skip half, which is what dominates real text |
| simdjson-style stage 1/2 parsing | Needs a kernel, not a surface — and stage 1 as written fails §3.1's rule 2, since it produces an index array rather than a scalar |
| SwissTable SIMD group probing | **SWAR VARIANT DONE** (below); the 16-wide vector probe needs a kernel and a workload to justify it |
| Sorting networks over vector registers | Fails §3.1's rule 2 — a network permutes its input, so it produces output rather than a scalar. It wants the first-class vector type §1.2 defers, not another fused kernel |
| SIMD `memcmp` | **DEFERRED, not blocked** — see the input-vs-needle rule below |

**What actually costs, now that the surface exists.** Not the vector body: the
ASSEMBLERS. §3.3a's rule is to check the assembler for every target a kernel is
about to be emitted on and land the encodings first, and the count is higher
than it looks — there are **eight backends and six assemblers**, because each
self-host backend has one of its own in Fern (`x86_native.fern`,
`arm64_native.fern`, `watbin.fern`) alongside the three in `internal/native`,
and the two `-backend ssa` legs read the `internal/native` pair. The first three
kernels each paid that cost separately and it was the dominant cost each time;
`__count_byte` paid nothing, because the encoding debt is per-INSTRUCTION-SET
rather than per-kernel and it is assembled from shapes the others bought.

**Which kernels are worth building** is answered by the rule §4 of that document
names: *a fused kernel pays when its vector length is the INPUT; it does not pay
when its vector length is the NEEDLE.* `memchr` scans a haystack, so it pays.
`memcmp` compares needle-length runs, and needles are short in nearly every real
caller (`","` is 1 byte, `"https://"` is 8, and Two-Way's `__substr_eq`
compares exactly needle-length runs), so at 8 bytes the whole compare fits in
one vector and the cost is call overhead. That is why it is deferred rather than
blocked, and it is the first question to ask of any candidate below.

Where a SIMD algorithm has a credible SWAR (SIMD-within-a-register, 64-bit-word)
variant, the row still says so — SWAR needs no kernel at all and is often 4–8×
over byte-at-a-time.

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
a prerequisite · **DEFERRED** reachable and decided against for now, with the
reason · **N/A** doesn't apply to Fern's model · **OK** current implementation
is already the right answer.

A row that once read "BLOCKED (SIMD)" now means one of two different things,
and they are worth telling apart: **needs a kernel** (mechanical — the contract
and six assemblers are in place, so it is a build item) or **deferred** (the
input-vs-needle rule says it would not pay).

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
| `Map` layout | **SwissTable-style ctrl bytes + SWAR group probe**, home-bucket fast path, unchanged linear-probe order | SwissTable + SIMD group probe | **DONE (SWAR, this pass)** — 1.45x miss-heavy / 1.15x hit-heavy near the load ceiling, parity at typical load (`map_probe_chain` gates it); a 16-wide group-probe kernel through the #6198 surface is the remaining step |
| String hash | **FNV-1a over 4-byte blocks + fmix32 avalanche** | wyhash / XXH3 | **DONE (this pass)** |
| Scalar hash | Wang mix | Fine | OK |
| Adversarial keys | **Per-process seed XORed into the FNV basis, default on** | SipHash, randomised seed | **DONE for offline attacks (this pass)**; SipHash still wanted against an online oracle |
| Tiny map | **Linear scan at or below 8 entries** | Linear scan below ~8 entries | **DONE (this pass)** |
| Checksum | — | xxHash / CRC32 | GAP |

The SWAR group probe landed in `core/map` without a kernel: a ctrl-byte
column (H2 tag / empty / tombstone) with an 8-byte wraparound mirror, scanned
8 buckets per `(x - 0x0101..) & ~x & 0x8080..` word. Probe ORDER stayed
linear, so bucket placement and delete's back-pointer walk were untouched.
Two costs surfaced by measuring: the group machinery loses to the scalar
compare on the ~1-bucket chains that dominate at ≤75% load (fixed by a scalar
home-bucket fast path), and the match written as a helper function costs
2-3 real calls per group (fixed by inlining with the broadcasts hoisted).

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
| Single-byte search | **`__memchr` / `__rmemchr`, vector on every backend** | SIMD `memchr` | **DONE (both directions)** — forward ~43x through `index_of`, backward 8.8x through `last_index_of` |
| Byte counting | **`__count_byte`, vector on every backend** | SIMD `memchr`-shaped count | **DONE** — 10.2x native x86-64. Adoption is TOTAL rather than a needle-length tier: with no cursor and no needle length there is no shape where the intrinsic and the loop differ |
| Backward search (`last_index_of`, `rsplit_once`, `rpartition`) | **Metered naive scan escalating to reverse Two-Way** | Reverse Two-Way | **DONE (this pass)** |
| `split` / `replace` / `find_all` / `count` | Routed through the Two-Way core | — | **DONE (this pass)** |
| Case-insensitive search | Naive | Case-folded Two-Way | GAP |
| `memcpy` / `memcmp` | Runtime helpers | Vectorised, alignment-aware | `memcmp` **DEFERRED by the input-vs-needle rule** — its favourable shape (long compares) is the uncommon one; `memcpy` is a kernel away, SWAR viable |

### Unicode

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| UTF-8 validation | **`__ascii_run` skips each ASCII run 16 bytes at a time**; the multi-byte arms stay a branch ladder in Fern | Höhrmann table DFA, then simdutf | **PARTLY DONE** — 0.22 → 13.8 GB/s on ASCII-heavy text. The split is deliberate: the per-length overlong and surrogate rules are branchy logic that would be duplicated across eight backends, and only the run BETWEEN sequences vectorises. The table DFA for the remaining arms is still unblocked |
| UTF-8 length / decode | Scalar | Scalar is fine below SIMD | OK |
| UTF-8 ↔ UTF-16 | `std/utf8` | simdutf | BLOCKED |

### Parsing

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| JSON | `std/json`, scalar | simdjson stage 1/2 | Needs a KERNEL, not a surface — the surface shipped |
| Lexer character classes | Branch chains in places | 256-entry lookup tables | **GAP, easy, applies to the compiler's own lexer** |
| CSV | Scalar | SIMD quote/delimiter scan | BLOCKED |
| Number parsing in parsers | Shared with `parse_float` | Eisel–Lemire | GAP (see above) |

Table-driven character classification is the part of the simdjson lesson that
survives without SIMD, and the compiler's own lexer is the beneficiary.

### Bit manipulation

| Primitive | Today | Best known | Verdict |
| --- | --- | --- | --- |
| `count_ones` | **Intrinsic** — `popcnt` / `cnt`+`addv` / `i32.popcnt` | `POPCNT` / NEON `CNT` / wasm `i32.popcnt` | **DONE**, hardware on all three |
| `leading_zeros` | **Intrinsic** — `lzcnt` / `clz` / `i32.clz` | `LZCNT` / `CLZ` / `i32.clz` | **DONE**, hardware on all three |
| `trailing_zeros` | **Intrinsic** — `tzcnt` / `rbit`+`clz` / `i32.ctz` | `TZCNT` / `RBIT`+`CLZ` / `i32.ctz` | **DONE**, hardware on all three |
| `byte_swap` | Software | `BSWAP` / `REV` | GAP |
| `rotate_left/right` | Software | `ROL`/`ROR` / wasm `rotl` | GAP |

These were 32- and 64-iteration software loops in all four integer modules —
the source comment gave the reason plainly: "no intrinsics surface in lang" —
and they got to hardware in two steps, each measured on x86-64. Branchless SWAR
first, needing no compiler work at all, at **4.3x** (3M iterations of all three
ops: 1250-1282 ms down to 278-299 ms, loop overhead inside both figures). Then
the `OpPopcount` / `OpClz` / `OpCtz` family and its lowerings, at a further
**3.9x** over the SWAR — the compiler project this row once called the endpoint,
now done. Details and the per-backend instruction table are below.

Anything built on them (hash mixing, bit-set iteration, `bit_length`) gets it
for free, which is what makes `byte_swap` and `rotate` the two rows still worth
taking: they are the same shape, and the op family they would join exists.

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
| Transcendentals | fdlibm kernels on every backend, ~1 ulp, agreement gated | RLIBM correctly-rounded | Narrowed; the accuracy-contract decision is still open |
| Date/time | `std/time` | Howard Hinnant's civil-date algorithms | Worth an audit |
| Compression | None | LZ4 / Zstd | N/A (not in the library) |
| Crypto | `std/crypto` | BLAKE3, hardware AES/SHA | GAP (hardware acceleration needs an intrinsic surface) |
| Parallel algorithms | None | Work stealing, parallel scan | N/A until a threading model exists |

The transcendental row carries a design question, not just an implementation
one. The divergence it was written about is closed — every backend emits the
same fdlibm kernels from one coefficient table, and `TestF64TranscendentalBackendsAgree`
pins them bit for bit — but agreement is not a promise about accuracy, and Fern
still has not made one: correctly-rounded results, a stated ULP bound, or only
that every target answers alike. The `fast_sin` / `sin` API split the essay
suggests is one way to make that explicit.

## Recommended order

Ranked by (value to real workloads) ÷ (implementation risk), given the
constraints above.

**Tier 1 — unblocked, high value**

1. ~~**Hardware bit intrinsics.**~~ Done — the IR op family landed and lowers on
   every backend, 3.9x over the SWAR it replaced. `byte_swap` and `rotate` are
   the same shape and are what is left of this row.
2. **Eisel–Lemire `parse_float`.** The sibling of the Dragonbox work; needs a
   128-bit high-multiply.
3. ~~Move `std/fuzz` onto the seeded generator~~ — done, see below.
4. ~~Small-map linear-scan path~~, ~~per-process hash seed~~, ~~SWAR group
   probe~~ — done, see below. What remains on `core/map` is SipHash for the
   seeded path (the seed closes offline precomputation; an online timing
   oracle can still recover an invertible FNV).
5. ~~Adaptive sort~~, ~~`random_int` modulo bias~~, ~~SWAR bit counting~~,
   ~~bit-counting intrinsics~~,
   ~~map string hash~~, ~~seeded PCG32~~ — done, see below.

**Tier 2 — unblocked, narrower**

6. ~~Reverse Two-Way for the backward-search family~~ — done, see below.
7. ~~SWAR group probing for `Map`~~ (the SwissTable idea, minus the vectors) —
   done: ctrl bytes plus an 8-bucket SWAR scan over the unchanged linear-probe
   order, behind a scalar home-bucket check. 1.45x miss-heavy / 1.15x hit-heavy
   near the load ceiling, parity at typical load.
8. ~~2-digit integer→string~~ — done, see below. SWAR string→int is blocked with #6200.
9. Table-driven lexer classification.
10. Magic-number constant division in the compiler.

**Tier 3 — was blocked on a vector surface; the surface shipped**

A SIMD surface in the IR was named here as the single prerequisite unblocking
simdjson-style parsing, simdutf validation, true SwissTable probing, vectorised
`memchr`/`memcmp`, and sorting networks — one project with the whole tier as its
payoff, not to be attempted piecemeal.

That framing was right and it has been paid off. Four kernels now ship on all
eight backends (constraint 1 above), so nothing on that list is blocked on the
surface any more, and what is left has sorted itself into three different
answers rather than one queue:

- **Done.** `memchr` in both directions, plus `__ascii_run` and `__count_byte`.
- **Decided against, with a measurement.** `memcmp` by the input-vs-needle rule;
  SwissTable's vector group probe, because the SWAR variant took most of the win
  and no map-bound workload has asked for the rest; sorting networks, which fail
  §3.1's rule 2 outright and want the deferred vector *type*, not a kernel.
- **A build item.** simdjson stage 1 and the remaining simdutf arms — each wants
  a kernel through the surface that exists, which is a different and much
  smaller kind of blocked than the one this tier was filed under.

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

**Both of those have since happened too.** `__memchr` shipped as the first
kernel and the performance-regression gate exists — `examples/bench/`
`string_find_byte`, `string_rfind_byte` and `ascii_scan` put each kernel's
vector path under `scripts/perf-bench`, whose retired-instruction counts repeat
to the digit, so a kernel silently returning to a byte loop moves them by most
of an order of magnitude. What the build revealed that this section did not
predict is above: the cost is the six assemblers, not the vector bodies.

## What landed in this pass

**Persistent collections (#6794).** `std/ordmap` / `std/ordset` (weight-balanced
tree with join-based set algebra and rank access), `std/pmap` / `std/pset`
(32-way HAMT with cached hashes and canonical collapse), and `std/pvec` (32-way
trie + tail). One value-returning API; the compiler's reuse pass gives the
unique path in-place updates and the shared path structural sharing. Measured
on x86-64 with 200,000 entries and a snapshot kept after every one of 2,000
further updates: `T[]` 3.12 s / 3.67 GB against `std/pvec` 0.013 s / 4.2 MB;
`core/map` OOM-killed against `std/pmap` 0.27 s / 14.8 MB. With nothing shared
the mutable structures still win by 5x (maps) to 28x (vector). Rows 36, 163,
177, 178. Design, the four compiler bugs it surfaced, and the rc gaps it still
works around: `docs/PERSISTENT-COLLECTIONS.md`.

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
(`OpPopcount` / `OpClz` / `OpCtz`) across all NINE backends plus the
interpreter. Nine rather than the eight the fused SIMD kernels reach, and the
difference is instructive: `internal/codegen/wasmssa` consumes `ssa.Func`
directly and has no string-helper table, so a kernel cannot reach it — but a
plain scalar op family lowers there like anywhere else.

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
| arm64 | `clz` | `rbit` + `clz` | `cnt` + `addv` |
| x86-64 | `lzcnt` | `tzcnt` | `popcnt` |

All three are hardware on all three backends now, and the two that were not are
worth recording because of what closed them. On x86-64 the blocker was the
baseline: `POPCNT` is SSE4.2 and Fern emits static binaries with no runtime CPU
dispatch, so selecting it is a promise the whole binary makes — taking the
declared Haswell baseline at its word, not a new decision. On arm64 the hardware
popcount lives on the SIMD side (`cnt` per byte, `addv` to sum), and the
in-process assembler `cmd/fern -target arm64-linux` uses by default could encode
none of `cnt`, `addv` or `rbit`. That gap closed as a **side effect of the SIMD
kernels** (#6198), which had to teach the same assembler its NEON surface for
`__memchr` — the encoding debt being per-instruction-set rather than per-caller
cuts both ways. Both still gain from being inline on the IR path rather than
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

**A native/self-host divergence in `split("")` was found, and is closed on the
register backends only.** `std/string.split` splits an empty separator by
CODEPOINT, but the self-host compiler lowers `.split()` to its own runtime
helper, and both copies of that helper — `rt_src_str_split` in `asmcore.fern`
(x86-64 + arm64) and `str_split_helper` in `wasm_ir.fern` (hand-written WAT) —
returned `[s]` instead. So `"abc".split("")` was `["a","b","c"]` natively and
`["abc"]` self-host-compiled. The comment recorded it as deliberate: it matched
the hand-written asm emitter, and no differential test covered empty separators.
Both halves of that rationale had expired — the hand-asm emitters were deleted,
and `internal/e2eselfhost/self_host_str_runtime_stdstring_parity_test.go` now
compares the helpers against the interpreter running `std/string` itself. That
test has NO wasm leg: `wasm_ir.fern`'s WAT copies of `split` / `lines` / `trim`
are still byte-based and still disagree with `std/string` (#8509).
