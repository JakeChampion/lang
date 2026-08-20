# SOTA stdlib implementation matrix — 2026 H2

The companion to `SOTA-STDLIB-BLUEPRINT.md`. The blueprint is an **audit**:
it records what the code does today and what the literature's best is. This
document is the **execution matrix**: the same territory turned into rows that
can be picked up, with a prerequisite, a phase, and an acceptance gate on each
one, plus an explicit verdict for every entry on the standard published
shortlist — including the ones Fern should *not* adopt.

The two are meant to be read together and to drift apart slowly:

| Document | Answers |
| --- | --- |
| `SOTA-STDLIB-BLUEPRINT.md` | What is the state of the art, and where does our code sit against it? |
| **this file** | Which of those rows are actionable *here*, in what order, blocked on what, proven by which gate? |
| `ATLAS-PLATFORM-PLAN.md` | What *substrate* do those rows need — SIMD shape, dispatch, allocator, concurrency — and in what order? |
| `STDLIB-ROADMAP.md` | What breadth is missing versus other languages' stdlibs? (surface, not algorithms) |
| `STRINGS-SOTA.md` | Deep dive on the string surface specifically. |

`ATLAS-PLATFORM-PLAN.md` landed the same day as this file, from a separate
external blueprint, and the two were written without sight of each other. They
converge — SIMD deferred, the allocator section largely N/A, concurrency gated
on a memory-model decision, `rotate`/`byteswap` the cheapest remaining
intrinsic, a benchmark harness the highest-value missing piece — which is worth
more than either document alone, since neither borrowed the conclusion. Where
they overlap, Atlas is authoritative on the **platform layer** (the vector
surface §R, dispatch, allocation §H, concurrency §M) and this file on the
**per-primitive rows**. Do not restate one in the other; link instead.

## How to read a row

Every row carries a verdict, and the verdict vocabulary is deliberately small:

- **SHIPPED** — Fern is at, or materially at, the state-of-the-art entry. The
  row exists so nobody re-does it.
- **GAP** — worth doing and **unblocked today**. These are the only rows that
  can be started without a decision or a prerequisite landing first.
- **BLOCKED:*x*** — needs *x* first. The prerequisite is named, never left as
  "needs infrastructure".
- **DECIDE:*x*** — the obstacle is a policy question, not an implementation
  one. Writing code before answering *x* wastes the code.
- **N/A:*reason*** — the row does not apply to Fern's execution model. This is
  the most important verdict in the document, because the published shortlists
  are written for C++ and Rust on x86-64 and a large fraction of them do not
  transfer.

"Site" is where the work lands, so a row can be assigned without a search.
Empty means the module does not exist yet.

## The five facts that re-rank the generic advice

Every verdict below is conditioned on these. They are the reason this matrix
is not a transcription of the published list.

**1. There is no SIMD — anywhere.** Not a vector type in `internal/ir`, not a
lowering in any of the three native backends or the three self-host ones, not
an intrinsic surface in the language. Verified, not assumed: the only match for
`SIMD` under `internal/ir` is a comment about stack alignment. This deletes the
top item from most published shortlists — simdjson stage 1/2, simdutf block
validation, SwissTable group probing as designed, vector `memchr`/`memcmp`,
sorting networks over vector registers. Those are **one project with one
payoff**, not eleven independent rows.

Where a technique has a **SWAR** variant (SIMD-within-a-register, over a plain
64-bit word), the row says so. SWAR needs no compiler work and typically buys
4–8× over byte-at-a-time.

**2. Memory is a bump arena plus Perceus reference counting, not a general
malloc.** Section H of the published list — mimalloc/jemalloc/snmalloc class
allocators, size classes, thread-local heaps — is mostly answered already, by
a different design: a 16 GiB `MAP_NORESERVE` bump arena, a large-tier freelist,
and RC with constructor reuse. The live questions here are **over-retention and
reuse** (`docs/SELFHOST-PERCEUS-REUSE.md`, issue #6127), not allocator
selection. A row that says "adopt mimalloc" is answering a question Fern does
not have.

**3. There are no threads.** `std/async` is single-threaded cooperative
concurrency over a poll-based readiness loop, not a work-stealing runtime.
Section M of the published list (atomics, MCS locks, Chase-Lev deques, MPMC
queues) and every "parallel *x*" row are downstream of a threading model that
does not exist and has not been decided. See `docs/MULTICORE-RESEARCH.md`.

**4. Six backends, so an intrinsic is never one change.** Any new primitive
lowered as an IR op needs `internal/ir` plus x86-64, arm64 and wasm on the
native side, *and* `asm_ir.fern` / `asm_arm64_ir.fern` / `wasm_ir.fern` on the
self-host side, plus the interpreter, or it is a parity gap. This is why
"just use `ROL`" is a project and `count_ones` took a whole pass. Budget
accordingly; a stdlib-only SWAR version that captures 80% is often the right
first move, and the bit-counting work is the worked example (SWAR first,
intrinsics later, measured 3.9× on the second step).

**5. The compiler is the biggest workload.** The self-hosted compiler is a
long-running, allocation-heavy Fern program and by far the most demanding
consumer of `std/string` and `core/map`. A string or map improvement compounds
into compile times, which is why substring search and map probing rank above
things that look more glamorous. It is also the only Fern program large enough
to make an algorithmic claim measurable end-to-end.

---

## A. Numeric conversion and arithmetic

| # | Primitive | Site | Today | Target | Verdict | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | f64 → shortest | `std/float` | Dragonbox (#6161) | Dragonbox / Schubfach | **SHIPPED** | — |
| 2 | f32 → shortest | `std/float` | Dragonbox, 64-bit cache | Dragonbox | **SHIPPED** | — |
| 3 | decimal → f64 | `std/float` | Eisel–Lemire + exact fallback (#6167) | Eisel–Lemire | **SHIPPED** | — |
| 4 | decimal → f32 | `std/float` | Via f64 then narrow | Direct f32 path | GAP (double-rounding, see §B) | 4 |
| 5 | hard float parse | `std/float` | Exact fallback present | Big-int fallback | **SHIPPED** | — |
| 6 | f64 → fixed | `std/float` | `to_string_prec` | Ryū Printf | GAP, low value | 4 |
| 7 | f64 → scientific | `std/float` | `to_string_prec` | Ryū Printf | GAP, low value | 4 |
| 8 | u64 → decimal | `core/int` | 2-digit table, exact-size buffer | Lemire multiply-shift | GAP — needs #12 | 1 |
| 9 | decimal → integer | `std/string` | Byte-at-a-time accumulate | SWAR 8-digit chunks | BLOCKED:raw-load (#6200) | 2 |
| 10 | hex → integer | `std/hex` | Byte-at-a-time | SWAR nibble fold | BLOCKED:raw-load (#6200) | 2 |
| 11 | divide by constant | compiler | Emitted as a divide | Magic-number reciprocal | GAP — compiler-side | 1 |
| 12 | modulo constant | compiler | Emitted as a divide | Reciprocal reduction | GAP — same change as #11 | 1 |
| 13 | arbitrary division | compiler | Hardware divide | Hardware divide | **SHIPPED** | — |
| 14 | mul_wide | compiler | Present (Dragonbox uses it) | `MUL`/`MULH` | **SHIPPED** | — |
| 15 | mul_high | compiler | Present | `MULH` | **SHIPPED** | — |
| 16 | popcount | `std/{i32,i64,u32,u64}` | `OpPopcount`; inline SWAR on native, `i32.popcnt` on wasm | Hardware popcount | **SHIPPED** — DECIDE:cpu-baseline for the instruction | 1 |
| 17 | clz | same | `OpClz` | `LZCNT`/`CLZ` | **SHIPPED** | — |
| 18 | ctz | same | `OpCtz` | `TZCNT`/`RBIT+CLZ` | **SHIPPED** | — |
| 19 | bit reverse | — | Absent | `RBIT` / table | GAP, narrow | 3 |
| 20 | rotate | — | Absent; `std/crypto` hand-rolls `__rotr` with two shifts and an or | `ROL`/`ROR`/`i32.rotl` | **GAP — best value in this section** | 1 |

**Rows 8 / 11 / 12 are one row.** Integer→string is five divisions per ten
digits today because no backend turns a constant divide into a multiply-and-
shift. Writing Lemire's reciprocal by hand in Fern means writing a 64-bit
multiply-high the backends then lower badly — so the fix belongs in the
compiler, and it pays out across every constant divide in every Fern program,
not just this one call site.

**Row 20 is the cheapest real win here.** `std/crypto` open-codes rotate-right
inside its SHA-256 compression function; every hash, PRNG mixer and checksum
that follows will do the same. `i32.rotl`/`rotr` exist in wasm and both native
ISAs have the instruction, so this is the same shape as the `clz`/`ctz` work
that already landed and can reuse its scaffolding.

**Row 16's asterisk.** `POPCNT` is SSE4.2 and Fern emits static binaries with
no runtime dispatch, so selecting it turns a sub-baseline CPU into a SIGILL,
not a slow binary. The project baseline is already stated as Haswell-class in
`CLAUDE.md`/`docs/BACKEND-PARITY.md` — so this is a matter of taking the
baseline at its word in codegen, not a new decision. On arm64 the blocker is
different and concrete: `cnt`/`addv`/`rbit` are not implemented in the
in-process assembler, which is also what row 19 needs.

## B. Elementary floating-point math

| # | Primitive | Site | Today | Target | Verdict | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| 21 | sqrt | `std/float` | `__sqrt_f64` builtin | Hardware sqrt | **SHIPPED** | — |
| 22 | fma | — | Absent | Hardware FMA | GAP | 4 |
| 23 | sin | `std/float` | libm native, polynomial on wasm | RLibm | DECIDE:accuracy-contract | 4 |
| 24 | cos | `std/float` | same | RLibm | DECIDE:accuracy-contract | 4 |
| 25 | tan | `std/float` | **`sin(x)/cos(x)`** | Direct approximation | **GAP — accuracy bug, not a speed row** | 4 |
| 26 | exp | `std/float` | libm native, polynomial on wasm | RLibm | DECIDE:accuracy-contract | 4 |
| 27 | log | `std/float` | same | RLibm | DECIDE:accuracy-contract | 4 |
| 28 | log2 | `std/float` | **`log(x) / ln2`** | Direct range reduction | **GAP — accuracy bug** | 4 |
| 29 | log10 | `std/float` | **`log(x) / ln10`** | Direct | **GAP — accuracy bug** | 4 |
| 30 | pow | `std/float` | `__pow_f64` builtin | Specialised exp/log | DECIDE:accuracy-contract | 4 |
| 31 | cbrt | `std/float` | Fern-level implementation | Correctly rounded | GAP | 4 |
| 32 | hypot | `std/float` | Fern-level, plus `hypot3` | Scaled, overflow-safe | Audit for overflow | 4 |
| — | f32 transcendentals | `std/float` | **Every one widens to f64, computes, narrows** | Direct f32 | **GAP — double rounding** | 4 |

This section is the one place where the published list and Fern's actual
problem genuinely diverge in *kind*. The list frames these as performance rows
("RLibm, SIMD, correctly rounded"). Fern's problem is **correctness and
consistency**, and it is visible in three ways that reading the source makes
obvious:

1. `tan` is `sin/cos`, `log2` is `log(x)/0.693…`, `log10` is `log(x)/2.302…`.
   Each of those is two rounded operations composed, so the result can be
   several ULP off where a direct implementation is under one.
2. Every `f32` transcendental computes in f64 and narrows. That is
   double rounding: a value that should round one way at f32 can round the
   other because it passed through f64 first.
3. **The same expression can give different answers on different backends
   today** — native calls libm, wasm runs a polynomial approximation
   (`wasm.exp_func` and siblings). This is not a rounding subtlety; it is a
   backend-observable difference in a language whose whole test strategy is
   differential.

So the prerequisite for this entire section is a **stated accuracy contract**,
and it is the only DECIDE in the matrix that blocks a whole section. Three
credible answers, in ascending cost: (a) "whatever the platform does",
documented, and the wasm/native divergence becomes legal; (b) a stated ULP
bound per function, enforced by a differential test against a reference; (c)
correctly rounded, RLibm-style, which is the only answer that makes results
backend-independent by construction. Fern's differential-testing culture
argues for (b) at minimum — (a) is what we have by accident and have never
written down.

Rows 25, 28, 29 and the f32 family are worth fixing **under any of the three
answers**, which makes them the ones to start with.

## C. Hashing and associative collections

| # | Primitive | Site | Today | Target | Verdict | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| 33 | HashMap | `core/map` | Open addressing, split key/value columns | SwissTable | BLOCKED:SIMD as designed; **SWAR group probe viable** | 1 |
| 34 | Large hash map | `core/map` | Same path at every size | F14-style grouped probing | Folds into #33 | 1 |
| 35 | Small hash map | `core/map` | **Linear scan at ≤ 8 entries** | Inline scan | **SHIPPED** | — |
| 36 | Ordered map | — | Absent | B-tree | GAP | 3 |
| 37 | Identity map | `core/map` | Wang mix on scalars | Integer hashing | **SHIPPED** | — |
| 38 | General hash | `core/map` | FNV-1a over 4-byte blocks + fmix32 | XXH3 / wyhash | BLOCKED:raw-load (#6200) | 2 |
| 39 | Adversarial hash | `core/map` | Per-process seeded FNV (#6194) | SipHash-1-3 | GAP — closes the online-oracle case | 1 |
| 40 | Cryptographic hash | `std/crypto` | SHA-256 only | BLAKE3 / SHA-512 / SHA-3 | GAP | 6 |
| 41 | Bloom filter | — | Absent | Blocked Bloom | GAP, narrow | 3 |
| 42 | Membership filter | — | Absent | Cuckoo filter | N/A:no-consumer | — |
| 43 | Cardinality | — | Absent | HyperLogLog | N/A:no-consumer | — |
| 44 | Frequency | — | Absent | Count-Min Sketch | N/A:no-consumer | — |

The published list's central point here — **do not use one representation for
every map size** — is already Fern's design, and it landed with numbers: at ≤ 8
entries `core/map` scans the entry array instead of hashing, measured 213 ms →
165 ms on a 6-entry string-keyed lookup benchmark. That is sound rather than
heuristic, because delete is swap-with-last so `[0, len)` is tombstone-free.

The remaining structural win is **SWAR group probing**: a SwissTable's
advantage is mostly cache behaviour, not the vector compare, and a 64-bit word
of control bytes tests 8 slots per iteration with
`(x - 0x0101…) & ~x & 0x8080…`. That captures the majority of the benefit with
no compiler work, and it is the row to do before any SIMD surface exists.

Rows 42–44 get **N/A:no-consumer** rather than GAP on purpose. They are
excellent data structures with no caller in Fern's workloads; adding them is
stdlib breadth (`STDLIB-ROADMAP.md`'s territory), not algorithmic progress.

## D. Sorting and selection

| # | Primitive | Site | Today | Target | Verdict | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| 45 | `sort` (unstable) | `core/cmp` | Absent — stable sort is the only option | pdqsort | GAP | 1 |
| 46 | `stable_sort` | `core/cmp` | **Adaptive natural-run merge** | Timsort | **SHIPPED** (MIN_RUN + galloping open) | 1 |
| 47 | Integer sort | `std/sort` | **Insertion sort, O(n²)**, on the in-place `i32[]` path | Radix / American flag | **GAP — biggest sort win** | 1 |
| 48 | Tiny sort (network) | — | Absent | Sorting network | BLOCKED:SIMD — scalar networks rarely pay | 5 |
| 49 | Tiny sort (insertion) | `core/cmp` | **Insertion below 32** | Insertion below ~24 | **SHIPPED** | — |
| 50 | Parallel sort | — | Absent | PPQSort / parallel pdqsort | N/A:no-threads | 6 |
| 51 | Parallel integer sort | — | Absent | Parallel radix | N/A:no-threads | 6 |
| 52 | `nth_element` | — | Absent | Introselect | GAP | 1 |
| 53 | `top_k` | — | Absent | Heap / quickselect hybrid | GAP — needs #162 | 3 |
| 54 | `median` | `std/array` | Full sort then index | Quickselect | GAP — falls out of #52 | 1 |

`std/sort`'s `sort_by`, `sort_by_i32_key` and `sort_key` still have the old
fixed bottom-up shape and did not get the adaptive treatment `core/cmp.sort`
did — that is the cheapest row in this section and it is pure code motion.

**Row 47 is the one with real headroom, and it is worse than "comparison
sort".** `std/sort`'s `sort_i32_inplace_asc` / `_desc` are **plain insertion
sorts** — O(n²), no cutoff, no run detection — over data whose keys are 32-bit
integers. They exist to dogfood `own` consuming parameters, which they do well,
but nothing about that requires the quadratic body. An LSD radix sort is O(n·k)
with no comparisons at all; even routing these through the adaptive
`core/cmp.sort` that already exists would be a large improvement for anything
past a few dozen elements. Unblocked, no compiler work, and `i32[]` sorting is
common enough to deserve the specialised path.

**Row 45 deserves care about the framing.** "pdqsort because Rust uses it" is
not sufficient here: Fern's sort is *stable*, and stability is an observable
contract several call sites depend on. Adding pdqsort means adding a second,
explicitly-unstable entry point — not replacing the existing one.

**A known compiler constraint shapes this whole section.** Calling a free
generic function from inside a loop body makes the self-host IR path bail, and
since the AST emitters were deleted a bail is a hard compile error. That is why
Timsort's MIN_RUN step is missing: extending short runs means calling an
insertion helper from inside the run loop. Anyone picking up #46 or #47 will
hit this. **Fixing the bail is worth more than any single sort row**, because
it is a standing constraint on how stdlib generics can be written at all.

## E. Strings and byte processing

| # | Primitive | Site | Today | Target | Verdict | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| 55 | `find_byte` | `std/string` | Scalar scan | SIMD `memchr` | BLOCKED:SIMD; **SWAR viable** | 1 |
| 56 | `find` | `std/string` | **Two-Way (Crochemore–Perrin)** | Two-Way + SIMD fast path | **SHIPPED** | — |
| 57 | `count_byte` | `std/string` | Routed through the search core | SIMD | BLOCKED:SIMD | 5 |
| 58 | `starts_with` | `std/string` | Anchored `__substr_eq` | Vector compare | **SHIPPED** (correct shape scalar) | — |
| 59 | `ends_with` | `std/string` | Anchored `__substr_eq` | Vector compare | **SHIPPED** | — |
| 60 | `memcmp` | runtime helper | Byte loop | Word-at-a-time, then SIMD | BLOCKED:raw-load; SWAR viable | 2 |
| 61 | `memcpy` | runtime helper | Byte loop | Size-specialised | BLOCKED:raw-load; SWAR viable | 2 |
| 62 | `memmove` | runtime helper | Byte loop | Overlap-aware | BLOCKED:raw-load | 2 |
| 63 | ASCII test | `std/string` | Byte loop | High-bit vector test | BLOCKED:SIMD; SWAR viable | 1 |
| 64 | ASCII lowercasing | `std/string` | Byte loop | SWAR bit manipulation | GAP — SWAR, unblocked | 1 |
| 65 | Base64 encode | `std/base64` | Scalar, per-character alphabet ladder | SIMD | BLOCKED:SIMD; table lookup unblocked | 1 |
| 66 | Base64 decode | `std/base64` | Scalar ladder | SIMD | BLOCKED:SIMD; table lookup unblocked | 1 |

Backward search deserves a note because it is the most instructive row that
already landed: `last_index_of` / `rfind` / `rsplit_once` / `rpartition` run a
naive scan **under a linear comparison budget**, escalating to reverse Two-Way
only when the budget is spent. Reverse Two-Way needs an O(n) copy of the
haystack, so it is a pessimisation on the common case (short separator near the
end) and a 190× improvement on the adversarial one (2.655 s → 0.014 s). The
budget is what lets both be true. **This is the shape to copy** whenever the
asymptotically-better algorithm has a setup cost.

Rows 65/66 illustrate the general SWAR-adjacent principle: `__b64_alphabet` is
a four-branch comparison ladder per character. Replacing it with a 64-byte
lookup table is not SIMD, needs nothing, and removes a branch per character.
The same is true of `__b64_decode_char`.

## F. Unicode

| # | Primitive | Site | Today | Target | Verdict | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| 67 | UTF-8 validation | `std/utf8` | **Branch-ladder length dispatch**, inlined continuation checks | Table DFA, then simdutf | **GAP — table DFA is unblocked** | 1 |
| 68 | UTF-8 → UTF-16 | `std/utf8` | Scalar | SIMD transcoding | BLOCKED:SIMD | 5 |
| 69 | UTF-16 → UTF-8 | `std/utf8` | Scalar | SIMD transcoding | BLOCKED:SIMD | 5 |
| 70 | UTF-8 → UTF-32 | `std/utf8` | Scalar | Hybrid | BLOCKED:SIMD | 5 |
| 71 | UTF-16 validation | `std/utf8` | Scalar | SIMD | BLOCKED:SIMD | 5 |
| 72 | Grapheme iteration | `std/unicode` | `graphemes` / `grapheme_count` / `reverse_graphemes` | UAX #29 | **SHIPPED** | — |
| 72a | Word segmentation | `std/unicode` | `word_segments` (lossless) / `words` / `word_count` | UAX #29 | **SHIPPED** | — |
| 73 | Case folding | `std/unicode` | `case_fold`, `eq_ignore_case`, packed tables | Unicode tables | **SHIPPED** | — |
| 74 | NFC | `std/unicode` | `nfc`, `is_nfc` with a quick-check range table | UAX #15 | **SHIPPED** | — |
| 75 | NFD | `std/unicode` | `nfd`, `is_nfd` | UAX #15 | **SHIPPED** | — |
| 76 | NFKC | — | Absent | UAX #15 | GAP, narrow | 3 |
| 77 | NFKD | — | Absent | UAX #15 | GAP, narrow | 3 |

Unicode is the section where Fern is **furthest ahead of the published
shortlist**, and the reason is worth stating: the list ranks SIMD transcoding
at P1 and normalisation at P1/P2, but a language that gets NFC wrong and
validation fast has the priorities backwards. Correct `nfc`/`nfd`, canonical
equality, grapheme clusters and case folding all exist. Two rows remain:
compatibility normalisation (genuinely narrow) and the SIMD tier.

The quick-check table in `is_nfc` — reject without allocating a normalised copy
— is the same fast-path/fallback discipline this document is arguing for,
arrived at independently.

**Row 67 is a correction to the blueprint, and it is a GAP rather than a
BLOCKED.** That document records UTF-8 validation as "byte-at-a-time DFA" and
concludes the scalar answer is already right. Reading `is_valid_utf8` shows
something else: a branch ladder over the leading byte with continuation checks
open-coded per length class, ~4 comparisons for the ASCII path and up to a
dozen for a 4-byte sequence. Höhrmann's table DFA is one table index and one
comparison per byte regardless of class, needs no vectors and no raw loads, and
is a straightforward scalar replacement.

The function's own header comment explains why this matters more than a
microbenchmark would suggest: validation sits on the **ingest path**, so every
byte arriving from a file, socket or FFI boundary pays it. The comment records
an earlier 32× fix on the same function (346 KB of ASCII JSON, 48 ms → 1.5 ms,
by not allocating an `Option[(i32,i32)]` per codepoint). An ASCII fast path is
the other unblocked half — the ladder currently pays its full branch sequence
per ASCII byte, and ASCII is what the ingest path mostly sees.

## G. Parsing

| # | Primitive | Site | Today | Target | Verdict | Phase |
| --- | --- | --- | --- | --- | --- | --- |
| 78 | JSON scanner | `std/json` | Scalar | simdjson stage 1 | BLOCKED:SIMD | 5 |
| 79 | JSON parser | `std/json` | Scalar recursive descent | simdjson stage 2 | BLOCKED:SIMD | 5 |
| 80 | JSON number | `std/json` | Delegates to `parse_float` (Eisel–Lemire) | Eisel–Lemire | **SHIPPED** | — |
| 81 | CSV scanner | `std/csv` | Scalar | SIMD delimiter detection | BLOCKED:SIMD | 5 |
| 82 | HTTP parser | `std/http` | Scalar | SIMD classification | BLOCKED:SIMD | 5 |
| 83 | Lexer classes | `internal/lexer`, self-host lexer | Branch chains in places | 256-entry lookup tables | **GAP — easy, and the compiler is the beneficiary** | 1 |
| 84 | Whitespace scan | lexers | Branch chains | SWAR / table | GAP | 1 |
| 85 | Identifier scan | lexers | Branch chains | Table-driven ASCII fast path | GAP | 1 |
| 86 | Quoted-string scan | `std/json`, lexers | Byte loop | SIMD quote/backslash detect | BLOCKED:SIMD; table-driven unblocked | 1 |

**Row 83 is the piece of the simdjson lesson that survives without vectors**,
and it lands on the compiler's own lexer, which is Fern's hottest parsing
workload by a wide margin. Table-driven classification is a few hundred bytes
of static table against a chain of comparisons per byte. Nothing blocks it.

Row 80 already got the benefit of #3 for free: `std/json` routes numbers
through `parse_float`, so Eisel–Lemire landing improved JSON parsing without
`std/json` changing at all. That is what a shared primitive is *for*, and it is
the argument for the foundations list further down.

## H. Memory allocation

| # | Primitive | Site | Verdict |
| --- | --- | --- | --- |
| 87 | General allocator | runtime | **N/A:different-model** — bump arena + large-tier freelist |
| 88 | Tiny allocation | runtime | **N/A:different-model** |
| 89 | Thread allocation | — | **N/A:no-threads** |
| 90 | Temporary allocation | runtime | **SHIPPED** — the arena *is* this |
| 91 | Fixed objects | runtime | **N/A:different-model** — RC constructor reuse covers the case |
| 92 | Compiler AST | runtime | **SHIPPED** — arena, and it is the motivating workload |
| 93 | Scratch memory | runtime | **SHIPPED** |
| 94 | SmallVec | — | GAP — inline storage for small arrays, phase 3 |
| 95 | SmallString | `internal/fernstring` | **SHIPPED** — SSO, see `docs/SSO-PLAN.md` |
| 96 | SmallMap | `core/map` | **SHIPPED** — the ≤ 8 scan path |
| 97 | Object allocation | runtime | **SHIPPED** — bump pointer |
| 98 | GC young generation | — | **N/A:no-tracing-GC** — Perceus RC, not generational |

This is the section where transcribing the published list would do the most
damage. Eight of twelve rows are N/A or already shipped **because Fern made a
different, deliberate choice**, and the two open questions in this area are not
in the list at all:

- **Over-retention**: seven unbounded leaks measured in the self-host runtime
  that native does not have (`FERN_LEAKCHECK=1`, issue #6127). Most are closed;
  ~108 KB over four shapes remained at the last measurement.
- **The rc==1 append cliff**: `__arr_push_shared_count()` /
  `__arr_push_shared_bytes()`. And the lesson attached to it — **rank by the
  weighted figure, never the count**. A whole-module compile crosses the cliff
  188 times copying 812 bytes (noise); one threaded accumulator over 20k
  appends copies 2.3 GB. Two rounds of optimisation work were scoped against
  the unweighted count and aimed at sites that could not have paid.

Anyone who arrives at section H wanting to "improve allocation" should be
pointed at those two, not at mimalloc.

## I. Big integers

| # | Primitive | Verdict |
| --- | --- | --- |
| 99–108 | Schoolbook / Karatsuba / Toom-Cook / NTT multiply, Burnikel-Ziegler division, Newton reciprocal, Montgomery, Barrett, binary & Lehmer GCD | **N/A:no-type** |

Fern has no arbitrary-precision integer type, so all ten rows are downstream of
a **language surface decision**, not an algorithm choice. If one is ever added,
the ordering in the published list is correct and uncontroversial (schoolbook →
Karatsuba around 20–40 limbs → Toom-Cook → NTT) and can be adopted wholesale.

The one piece of this section Fern *does* need is already covered elsewhere:
128-bit multiply-high, which Dragonbox and Eisel–Lemire both use, and which
exists.

## J. Random numbers

| # | Primitive | Site | Today | Verdict |
| --- | --- | --- | --- | --- |
| 109 | Default RNG | `std/rand` | OS CSPRNG, with seeded PCG32 alongside | **SHIPPED** |
| 110 | Fastest RNG | `std/rand` | PCG32 (`rng_next`) | **SHIPPED** |
| 111 | Deterministic RNG | `std/rand` | PCG32, explicit threaded state | **SHIPPED** |
| 112 | Parallel RNG | — | Absent | **N/A:no-threads** |
| 113 | Cryptographic RNG | `std/rand` | OS CSPRNG | **SHIPPED** |
| 114 | Gaussian | — | Absent | GAP — Ziggurat, narrow |
| 115 | Arbitrary distribution | — | Absent | GAP, narrow |
| 116 | Weighted choice | — | Absent | GAP — alias method, narrow |

Fully shipped where it matters, including the row the published list does not
have: **bounded draws are debiased** (Lemire's nearly-divisionless method), in
`std/rand`, `math.random_int` and `std/sim`. That was a correctness fix — a bare
`u % range` made `shuffle` produce non-uniform permutations.

The API shape here is worth carrying forward as precedent: the natural design
is a handle with interior mutability (`Rng { state: Cell[i64] }`), and **`Cell`
does not lower on the self-host IR path**, so state is threaded explicitly
(`rng_next(state) -> (state', value)`) with `*_seeded` wrappers hiding it. Any
future stateful stdlib type will hit the same wall. `std/sim` is still built on
`Cell` and is therefore **uncompilable by the self-host compiler** — a live
hole, not a stylistic note.

## K. Compression

| # | Primitive | Verdict |
| --- | --- | --- |
| 117–122 | LZ4, Zstandard, Brotli, DEFLATE, gzip, streaming | **N/A:not-in-library** → GAP if adopted, phase 6 |

Nothing exists. The decision to make first is not which algorithm but
**whether compression belongs in the stdlib at all** versus a package — see
`docs/PACKAGE-MANAGEMENT-SOTA.md`. If it lands, DEFLATE/gzip first for
compatibility (HTTP `Content-Encoding` is the actual consumer, via `std/http`
and `std/fetch`), and the streaming state machine (#122) is the row that
constrains the API and should be designed first, not retrofitted.

## L. Cryptography

| # | Primitive | Site | Today | Verdict |
| --- | --- | --- | --- | --- |
| 123 | BLAKE3 | — | Absent | GAP, phase 6 |
| 124 | SHA-256 | `std/crypto` | Pure Fern, `u32[]` state | **SHIPPED** — hardware acceleration BLOCKED:SIMD |
| 125 | SHA-512 | — | Absent | GAP |
| 126 | SHA-3 | — | Absent | GAP |
| 127 | AES-GCM | — | Absent | BLOCKED:no-AES-intrinsics |
| 128 | ChaCha20-Poly1305 | — | Absent | **GAP — the right first AEAD for Fern** |
| 129 | X25519 | — | Absent | GAP, phase 6 |
| 130 | Ed25519 | — | Absent | GAP, phase 6 |
| 131 | OS CSPRNG | `std/rand` | Present | **SHIPPED** |
| — | HMAC / PBKDF2 / HKDF / HOTP / TOTP | `std/crypto` | Present, constant-time compare | **SHIPPED** |

`std/crypto` is a SHA-256 tower: hash, HMAC, PBKDF2, HKDF, HOTP/TOTP, and a
constant-time `consteq`. There is **no AEAD, no public-key primitive, and no
second hash family**.

Row 128 over row 127 specifically because of constraint 1: AES without AES-NI /
ARMv8 crypto extensions is both slow and hard to make constant-time in a
high-level language, while ChaCha20 is add-rotate-xor over 32-bit words — which
is exactly what Fern can express well, and which **gets faster the moment row 20
(rotate) lands**. That dependency is the reason row 20 is ranked where it is.

The published list's closing advice on this section is the most important line
in it and is adopted verbatim: **prefer audited implementations over inventing
anything**. For Fern that means porting a known-good reference with its test
vectors, not writing a novel one, and treating constant-time behaviour as a
correctness property with a test, not a comment.

## M. Concurrency

| # | Primitive | Verdict |
| --- | --- | --- |
| 132 | Atomics | **N/A:no-threads** |
| 133 | Mutex | **N/A:no-threads** |
| 134 | Contention (futex) | **N/A:no-threads** |
| 135–137 | SPSC / MPSC / MPMC queues | **N/A:no-threads** |
| 138 | Work stealing (Chase-Lev) | **N/A:no-threads** |
| 139 | MCS / CLH locks | **N/A:no-threads** |
| 140 | Semaphore | **N/A:no-threads** |
| 141 | Barrier | **N/A:no-threads** |
| 142 | Timer | **SHIPPED** — `timer_fd` / `wasm_timer_pollable` on the readiness path |

Every row here is gated on a decision Fern has not made. `docs/MULTICORE-
RESEARCH.md` is where that decision goes; until it is made, implementing a
Chase-Lev deque is writing a data structure with no scheduler to put it in.

This is also where the published list's Chase-Lev entry (its #15 top-20 pick)
falls out of Fern's ranking entirely — not because it is a bad algorithm but
because the prerequisite is a runtime model, not a work item.

## N. I/O

| # | Primitive | Site | Today | Verdict |
| --- | --- | --- | --- | --- |
| 143 | Linux async I/O | native backends | **`poll(2)` / `ppoll(2)`** | GAP → io_uring, phase 6 |
| 144 | Windows async I/O | — | No Windows target | **N/A:no-target** |
| 145 | BSD/macOS | arm64-darwin | **`kevent(2)`** — `emitPollRuntimeKqueue` | **SHIPPED** (#6297) |
| 146 | Vector I/O | — | Absent | GAP, narrow |
| 147 | Zero-copy send | — | Absent | GAP — `sendfile`, narrow |
| 148 | Zero-copy file copy | — | Absent | GAP — `copy_file_range`, narrow |
| 149 | Mapped files | runtime | `mmap` used for the arena, not exposed | GAP, narrow |
| 150 | Async timers | runtime | Present on the readiness path | **SHIPPED** |

**Row 145 closed while this document was being written.** It was drafted as
the section's one act-now row: arm64-darwin is a supported target
(`CLAUDE.md` lists it, CI runs it on `macos-latest`) and `__fern_poll` was a
−1 stub there, so everything layered on readiness — `tcp_serve_deadline`,
`std/async`'s `gather` / `race` / `with_deadline` — degraded silently rather
than erroring. #6297 ported it (`emitPollRuntimeKqueue`, `kevent(2)` mirroring
the Linux `ppoll` path), so the parity hole is gone and §N has no out-of-phase
row left. The reasoning survives the fix and is worth keeping: a stub on a
supported target outranks a throughput row on a path that already works.

Row 143 is genuinely valuable for the edge-server workload, but `poll(2)` is
correct and adequate at the connection counts a short-lived edge handler sees.
It is a phase-6 row, after the model questions are settled.

## O. Date and time

| # | Primitive | Site | Today | Verdict |
| --- | --- | --- | --- | --- |
| 151 | Date → day number | `std/time` | **`__days_from_civil`** (Hinnant) | **SHIPPED** |
| 152 | Day number → date | `std/time` | **`__civil_from_days`** (Hinnant) | **SHIPPED** |
| 153 | Day of week | `std/time` | Arithmetic from the serial day | **SHIPPED** |
| 154 | ISO week | `std/time` | Arithmetic | **SHIPPED** |
| 155 | Leap year | `std/time` | Arithmetic | **SHIPPED** |
| 156 | Days per month | `std/time` | Table + arithmetic | **SHIPPED** |
| 157 | Timezone lookup | — | Absent — UTC only | **GAP — the only real gap in this section** |
| 158 | Instant arithmetic | `std/time` | Integer seconds | **SHIPPED** — DECIDE:nanosecond-resolution |

`std/time` already uses Howard Hinnant's civil-date algorithms, which is
exactly the published list's recommendation, and the blueprint's "worth an
audit" verdict can be closed: it was verified by reading, not assumed.

Two things remain. **Timezones** are a data problem (the IANA database) more
than an algorithm one, and the interesting design question is how the database
ships — embedded via `internal/embed`, read from the host, or a package. And
row 158's resolution question: `Instant` is second-based, while the published
list assumes integer nanoseconds. Widening it later is a breaking change, so it
is cheaper to decide now than to migrate.

## P. Advanced collections

| # | Structure | Site | Verdict |
| --- | --- | --- | --- |
| 159 | BitSet | — | **GAP — highest-value missing structure**, phase 3 |
| 160 | Sparse bitset | — | Roaring — GAP after #159 |
| 161 | Deque | — | GAP — circular buffer, phase 3 |
| 162 | Priority queue | — | GAP — d-ary heap, phase 3 |
| 163 | Ordered set | — | GAP — B-tree, phase 3 |
| 164 | Interval tree | — | N/A:no-consumer |
| 165 | Radix trie | — | N/A:no-consumer |
| 166 | Prefix map | — | N/A:no-consumer |
| 167 | Intern table | self-host compiler | Present in the compiler, not the stdlib — GAP:promote |
| 168 | Symbol table | self-host compiler | Same — GAP:promote |

**Row 159 first, and by a distance.** A BitSet is the substrate for dataflow
analysis in the compiler (which is a Fern program, constraint 5), for `std/set`
over small integer domains, for the sieve-shaped code in `std/fuzz` and
`std/sim`, and for row 160. It is also the structure that gains the most from
the bit intrinsics that already landed — `count_ones` / `trailing_zeros` are
exactly what iteration over a bitset needs, and they are already single IR ops
on all six backends. The prerequisite is done; the consumer is not written.

Rows 167/168 are a **promotion**, not an implementation: the self-host compiler
already has interning (`docs/SELFHOST-SYMBOL-INTERNING.md`). Lifting it into
the stdlib gives every Fern program the structure and gives the compiler one
fewer bespoke component — the erasure principle in `CLAUDE.md` applied to a
data structure.

## Q. Compiler and data-structure primitives

| # | Structure | Verdict |
| --- | --- | --- |
| 169 | Arena | **SHIPPED** — the runtime allocator |
| 170 | Bump allocator | **SHIPPED** |
| 171 | SparseSet | GAP — pairs with #159, phase 3 |
| 172 | BitSet | = #159 |
| 173 | Interner | = #167 |
| 174 | Rope | N/A:no-consumer — the LSP reparses whole files |
| 175 | Piece table | N/A:no-consumer |
| 176 | Gap buffer | N/A:no-consumer |
| 177 | Immutable vector | DECIDE:value-semantics — interacts with Perceus reuse |
| 178 | Persistent map | DECIDE:value-semantics |
| 179 | Hash-consing | GAP, narrow — the interner is the useful 80% |

Row 177/178 get DECIDE rather than GAP for a Fern-specific reason worth
spelling out: **persistent structures and Perceus constructor reuse solve
overlapping problems**. RC already gives in-place mutation when the refcount is
1, which is most of what a persistent vector's structural sharing buys, without
the indirection. Adding HAMTs before understanding that interaction risks
shipping a slower structure that looks more sophisticated. Measure the RC path
first (`FERN_LEAKCHECK=1`, `__arr_push_shared_bytes()`), then decide.

Rows 174–176 are N/A because the LSP's model is whole-file reparse
(`docs/IDE-COMPILATION-RESEARCH.md`); an editor buffer structure has no caller.
If incremental reparsing is ever adopted, these come back as GAP together.

## R. SIMD layer

| # | Primitive | Verdict |
| --- | --- | --- |
| 180 | Portable vectors | **BLOCKED:the-project-itself** — this is the prerequisite |
| 181 | Vector load/store | Same |
| 182 | Vector compare | Same |
| 183 | Vector min/max | Same |
| 184 | Vector shuffle | Same |
| 185 | Vector lookup | Same |
| 186 | Vector compress/expand | Same, and the least portable |
| 187 | Vector popcount | Same |
| 188 | Runtime dispatch | **DECIDE:static-binaries** — see below |

The published list ranks the portable SIMD layer at P0 and says the stdlib
should be written against it. **For Fern that is a single project, and it is the
single highest-leverage item in the entire matrix** — it is the named
prerequisite on 19 rows above.

Its scope is honest to state: vector types in `internal/ir`, plus lowering in
three native backends *and* three self-host backends (constraint 4), plus
SSE2/AVX2, NEON and wasm `v128` instruction selection, plus the assembler work
on the arm64 side where `cnt`/`addv`/`rbit` are not yet implemented. It should
be evaluated **as one project with that whole blocked tier as its payoff**, and
never attempted piecemeal — a half-vector-surface that unblocks `memchr` and
nothing else is the worst outcome available.

Row 188 carries a decision that has to be made *before* the project, not
during: Fern emits **static binaries with no runtime CPU dispatch**, which is
why `POPCNT` is not selected on x86-64 today. A vector layer either (a) targets
the stated Haswell baseline statically, which is simple and leaves AVX-512 on
the table forever, or (b) introduces runtime dispatch, which is a change to how
Fern links and starts up — and startup time is one of the two workloads the
language is built around. That trade is a language decision, not a codegen one.

---

## The fast-path contract

Adopted from the published list, because it is the right convention and Fern
already does it inconsistently. **Every stdlib primitive with a dispatch or a
fallback documents four things in its header comment:**

```
FAST PATH   what happens for the common input
GUARANTEE   the correctness and complexity promise
FALLBACK    what happens for pathological input
DISPATCH    how the specialisation is selected
```

Worked example, `parse_float` (`std/float`) — real, not hypothetical:

```
FAST PATH   Eisel-Lemire: 128-bit multiply against a power-of-ten cache
GUARANTEE   correctly rounded IEEE-754 double, always
FALLBACK    exact arbitrary-precision comparison when the 128-bit product
            cannot decide the rounding direction
DISPATCH    scalar only; no CPU specialisation exists
```

The convention earns its keep in two places. It makes an **unstated fallback
impossible to ship** — writing "FALLBACK: none" forces the author to notice
they have an unbounded worst case, which is what the backward-search budget and
the `is_nfc` quick-check both exist to avoid. And it makes the DISPATCH line
the natural home for the threshold, which is the number reviewers most need and
most often cannot find.

## Threshold ledger

Every dispatch threshold in the stdlib, with its justification. **A threshold
without a measurement is a guess**, and the ones below are marked accordingly.

| Threshold | Value | Site | Basis |
| --- | --- | --- | --- |
| Insertion sort cutoff | 32 | `core/cmp` | Measured against merge rounds |
| Small-map linear scan | ≤ 8 entries | `core/map` | Measured: 213 ms → 96 ms standalone, 213 → 165 in-map |
| Substring search dispatch | 0 / 1 / ≥ 2 bytes | `std/string` | Structural: gap / memchr-shape / Two-Way |
| Reverse search budget | Linear in haystack | `std/string` | Structural: bounds the naive scan before the O(n) copy |
| Hash block size | 4 bytes | `core/map` | Limited by the absence of an 8-byte load (#6200) |
| Digit-pair table | 2 digits | `core/int` | Measured: 0.721 s → 0.463 s on 4M conversions |
| **Radix vs comparison sort** | **unset** | `std/sort` | Row 47 — needs measuring, expect n ≈ 64–256 |
| **SWAR group probe width** | **unset** | `core/map` | Row 33 — 8 slots per 64-bit word is the natural unit |
| **Karatsuba crossover** | **n/a** | — | No bigint type |

The unset rows are the deliverable of their phase-1 work item, not an
afterthought: a row is not done until its threshold is a measured number with
the measurement recorded.

## Twelve foundations, re-derived

The published list proposes twelve foundational pieces from which the rest of
the library follows. Six of them do not survive contact with Fern's model, and
the substitutions matter more than the agreements:

| # | Published foundation | Fern verdict |
| --- | --- | --- |
| 1 | Portable SIMD | **Agreed** — and it is the pivot of the whole matrix (§R) |
| 2 | CPU feature detection / dispatch | **DECIDE** — conflicts with static binaries + fast startup |
| 3 | Arena allocator | **Already the runtime** |
| 4 | General allocator | **N/A** — arena + RC is the model |
| 5 | Small-object machinery | **Partly shipped** — SSO and small-map exist; SmallVec does not |
| 6 | Bit intrinsics | **Shipped** for clz/ctz/popcount; **rotate/byteswap missing** (row 20) |
| 7 | Fast byte/string primitives | **Agreed** — Two-Way shipped, `memchr`/`memcmp` open |
| 8 | Fast integer/float conversion | **Shipped** — Dragonbox + Eisel–Lemire + digit-pair tables |
| 9 | Swiss-style hash infrastructure | **Agreed, as SWAR** (row 33) |
| 10 | Lock-free primitives | **N/A:no-threads** |
| 11 | Zero-copy buffer abstraction | **Agreed — and missing.** See below |
| 12 | Benchmark + correctness framework | **Half shipped** — `std/test` + `std/fuzz` exist, benchmarking does not |

Two of these deserve promotion above where the published list puts them.

**Foundation 11, a raw-load/zero-copy surface, is Fern's actual bottleneck**
and the list treats it as a convenience. It is the named blocker on rows 9, 10,
38, 60, 61 and 62 — SWAR `parse_int`, wyhash/XXH3, `memcmp`, `memcpy` — because
all of them need an unaligned 8-byte load out of a string, and the language has
no way to express one (#6200). That is a **language design question about
exposing string data pointers**, and answering it unblocks more measured
performance than any single algorithm in this document.

**Foundation 12's missing half is benchmarking.** Fern has a test runner
(`std/test`, TAP-13) and a fuzzer (`std/fuzz`, seeded and replayable) but no
benchmark harness, so every measurement quoted in this document and in the
blueprint was taken by hand with a bespoke program. That is why some rows have
numbers and others have adjectives. A benchmark harness in `std/test`'s shape
is the cheapest way to make the rest of this matrix self-verifying, and it is
phase 0 for exactly that reason.

## Phases

Re-sequenced for Fern. The published list's Phase 0 ("ABI + SIMD + allocator")
is wrong here on all three counts: the ABI exists, the allocator is not the
problem, and SIMD is a mid-project pivot rather than a starting point.

**Phase 0 — Make the matrix self-verifying.** No new algorithms. A benchmark
harness (foundation 12); the fast-path contract applied to the primitives that
already have dispatch; the threshold ledger's unset rows identified. Exit gate:
a performance claim can be reproduced by running a command instead of writing a
program.

**Phase 1 — Unblocked scalar and SWAR wins.** Rotate/byteswap intrinsics (20),
SWAR map group probe (33), SipHash for seeded maps (39), radix integer sort
(47), `sort_by` adaptivity + unstable sort + quickselect (45, 52, 54), SWAR
`memchr` and ASCII ops (55, 63, 64), base64 table lookup (65, 66), UTF-8 table
DFA + ASCII fast path (67), table-driven lexer classification (83–86),
magic-number constant division (11, 12, and therefore 8). Every one of these is
startable today. Exit gate: each row's threshold measured and recorded.

The through-line of this phase is that **six of its rows are the same change**:
replace a chain of comparisons per byte with one table index. Base64's
alphabet ladder, the lexer's character classes, the JSON string scanner and
UTF-8 validation's leading-byte ladder are four instances of it, and doing them
as one pass shares both the idiom and the benchmark.

**Phase 2 — The raw-load decision.** Answer foundation 11: can Fern express an
unaligned 8-byte load out of a string, and under what safety story? Then
harvest rows 9, 10, 38, 60, 61, 62 together — they are one prerequisite and six
consumers, which is what makes this a phase rather than a row.

**Phase 3 — Missing data structures.** BitSet (159) first, then SparseSet
(171), deque (161), d-ary heap (162) and thus `top_k` (53), B-tree ordered
map/set (36, 163), SmallVec (94), interner promotion (167, 168). Roaring (160)
after BitSet has a caller. Exit gate: the compiler uses at least BitSet and the
interner, so the structures have a demanding consumer rather than only tests.

**Phase 4 — The numeric accuracy contract.** Answer DECIDE:accuracy-contract,
then fix `tan`, `log2`, `log10` and the f32 double-rounding family regardless
of which answer wins, then reconcile the wasm-vs-libm divergence to whatever
the contract now promises. Exit gate: a differential test across all backends
that the current code would fail.

**Phase 5 — The vector surface.** One project (§R), scoped with its whole
blocked tier as the payoff, with the static-dispatch decision (188) taken
first. This is where simdjson, simdutf, true SwissTable probing, vector
`memchr`/`memcmp` and sorting networks all land — nineteen rows behind one
gate.

**Phase 6 — Model-gated breadth.** Threading (unblocking all of §M and the
parallel-sort rows), io_uring (143), compression (§K), crypto breadth (123,
125–130), timezone data (157). Each needs its own decision first; none is
blocked on anything in phases 0–5.

**Out of phase.** This slot held arm64-darwin readiness (145) — a parity hole
on a supported target, not a performance row, and so not something to put
behind five phases of work. #6297 closed it. The slot stays because the rule
that put it here still applies: a stub or a divergence on a supported target
jumps the phase order, because it is a bug, not an optimisation.

## The top ten, re-ranked for Fern

The published list's top twenty, filtered through the five constraints. Nine of
its entries are N/A or already shipped; what remains, reordered by
(value to Fern's real workloads) ÷ (risk):

| Rank | Item | Why it moved |
| --- | --- | --- |
| 1 | **Rotate/byteswap intrinsics** (20) | Not on the published list at all. Cheapest real win, unblocks ChaCha20 and every hash mixer |
| 2 | **BitSet** (159) | The intrinsics it needs already landed; the compiler is a waiting consumer |
| 3 | **Radix integer sort** (47) | Largest single algorithmic gap that is unblocked |
| 4 | **SWAR map group probe** (33) | SwissTable's cache win without the vectors; compiler-critical |
| 5 | **Table-driven byte classification** (67, 83–86, 65, 66) | The surviving piece of the simdjson lesson; one idiom, six call sites, ingest path and compiler both benefit |
| 6 | **The raw-load decision** (fdn. 11) | One decision, six blocked rows behind it |
| 7 | **Float accuracy contract** (§B) | The only backend-observable *correctness* divergence in the matrix |
| 8 | **Magic-number constant division** (8, 11, 12) | One `internal/ir` peephole every backend inherits; pays out on every constant divide, and unblocks Lemire int→string |
| 9 | **Benchmark harness** (fdn. 12) | Makes every other row provable |
| 10 | **The vector surface** (§R) | Highest ceiling, highest cost; deliberately not first |

Dropped from the published top twenty and why: Eisel–Lemire and Dragonbox
(shipped), SwissTable as designed and simdjson/simdutf and parallel sort
(blocked, phase 5/6), arena allocation and small-string optimisation (shipped),
mimalloc (N/A), Chase-Lev (N/A:no-threads), Montgomery arithmetic (N/A:no
bigint type), Roaring (no consumer yet).

## Maintaining this document

Two habits, both learned the expensive way and both recorded in the blueprint:

**An audit is a photograph.** The blueprint's `parse_float` row read "GAP —
highest-value numeric item" for an entire work pass while Eisel–Lemire was
landing on main independently. Before starting any row here, **re-verify it
against current `main`** rather than against this file. The rows carry a site
column precisely so that check is a single `grep`.

**Verdicts are claims, and claims get checked by reading code.** Every SHIPPED
and N/A above was verified by reading the module, not inferred from a
changelog: `std/time` really does use Hinnant's algorithms, `std/crypto` really
does stop at SHA-256, `internal/ir` really has no vector type, `log2` really is
`log(x)/ln2`, `sort_i32_inplace_asc` really is quadratic. Two rows changed
verdict during this pass purely from reading — UTF-8 validation is a branch
ladder rather than the DFA the blueprint records (row 67, BLOCKED → GAP), and
integer sort is worse than "comparison sort" (row 47). Both had stood as
settled. When a row changes, change the verdict *and* say what was read.

When a row lands, it moves to SHIPPED here **and** the corresponding entry in
`SOTA-STDLIB-BLUEPRINT.md` gains a "what landed" paragraph with the measurement
in it. Two documents, one fact each, and neither one silently rots.
