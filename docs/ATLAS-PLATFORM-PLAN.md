# Atlas, reconciled against Fern

**Status:** decision record + sequenced plan (2026-08-06). Supersedes nothing;
it is the platform-layer companion to `docs/SOTA-STDLIB-BLUEPRINT.md`, which
surveys the *algorithms*. This document is about the *substrate* they need.

The input was an external blueprint ("Project Atlas") for a state-of-the-art
systems language: twelve phases, from a portable SIMD layer and CPU dispatcher
through allocators, strings, collections, concurrency, io_uring, compression,
and crypto.

**Provenance, because it changes how much weight the list carries:** it is
LLM-generated and was written with **no knowledge of Fern** — a generic
shortlist of what a modern systems language ought to contain, not an
assessment of this one. That makes it useful as a *checklist of the field* and
worthless as a *plan of record*, and it explains the pattern in §4: the items
that misfire do so because they encode assumptions (a malloc heap, shared-
memory threads, a Windows target, x86-first hardware) that a generic list
cannot know Fern does not share. Read every row below as "what the field
generally does" and nothing more.

Its organising thesis:

> The compiler, runtime and standard library are one optimization unit.

That thesis is right, and it is already Fern's operating assumption — the
self-hosted compiler is the standard library's most demanding consumer, so a
`std/string` or `core/map` win compounds into compile times rather than
showing up only in a benchmark. Nothing below argues with the philosophy.

What this document does is take the blueprint's *ordering* seriously enough to
check it against the code, because a phase order written for a C++/Rust-shaped
language inverts in five places for Fern. Each inversion below is stated with
the evidence that produced it. The short version:

| Atlas phase | Verdict for Fern |
| --- | --- |
| 0 — CPU feature detection + dispatcher | **Not needed at the 128-bit tier.** Deferred to the 256-bit tier. |
| 0 — portable SIMD *type* (`Vec32<u8>`) | **Wrong first shape.** Fused intrinsics deliver the payoff at a fraction of the cost. |
| 1 — allocator family (arena/bump/pool/general) | **Done, and partly rejected on purpose.** Fern is not in the malloc world. |
| 2 — primitive intrinsics | **Mostly done.** `byteswap` / `rotate` are the only remaining rows. |
| 3 / 4 / 5 — strings, numbers, collections | **Largely done**, ahead of the blueprint's own numbers in places. See `SOTA-STDLIB-BLUEPRINT.md`. |
| 7 / 8 — concurrency primitives, io_uring | **Blocked on a memory-model decision, not on implementation.** |
| 12 — compiler reuses the stdlib | **Already true, and more aggressively than Atlas proposes.** |
| Testing — perf regressions fail CI | **Genuinely missing. The highest-value item Atlas contributes.** |

---

## 1. The five inversions

### 1.1 The CPU dispatcher is unnecessary — because the baselines already promise SIMD

Atlas makes feature detection and runtime dispatch the foundation everything
else sits on:

```
fn memcpy(...) { switch(cpu) { AVX512 / AVX2 / NEON / Scalar } }
```

That is the right design for a library shipping one binary to unknown
hardware. Fern is not in that position. Per `CLAUDE.md ▸ Targets`, the
baselines are *declared*, and binaries are static with no runtime dispatch —
"a selected instruction is a hard requirement, not a fast path":

| Target | Declared baseline | 128-bit SIMD | 256-bit SIMD |
| --- | --- | --- | --- |
| x86-64 | Haswell-class, **SSE4.2 + BMI1** assumable | **guaranteed** (SSE2/SSE4.2) | *not* assumable — AVX2 is outside the declared set |
| arm64 | plain ARMv8-A, **Advanced SIMD included** | **guaranteed** (NEON) | n/a (SVE is a separate tier) |
| wasm | wasmtime v46.0.1 pinned | **guaranteed** (`v128`, standardised, on by default) | n/a |

So the entire 128-bit tier — which is where `memchr`, `memcmp`, UTF-8
validation, ASCII case conversion, SwissTable probing, and JSON structural
classification all live — needs **no detection and no dispatch at all**. It is
statically available on every target Fern supports. Building a dispatcher
first would be paying the cost of a mechanism whose only consumer does not
exist yet.

Dispatch becomes necessary exactly at the point Fern wants AVX2/AVX-512 on
x86-64 or SVE/RVV elsewhere, because those *are* outside the declared
baselines. That is a real future tier with a real prerequisite — and note it
is a **project decision, not a codegen one** (`docs/BACKEND-PARITY.md`): the
alternative to a dispatcher is raising the declared baseline, which is
cheaper and may well be the right answer. Either way it is sequenced *after*
the 128-bit tier, not before it.

One sharp edge worth recording, since it is the failure mode a dispatcher
exists to prevent: on x86-64 `LZCNT`/`TZCNT` **fail silently** below the
baseline — same opcodes as `bsr`/`bsf` plus an `F3` prefix the older CPU
ignores — where `POPCNT` faults loudly. A baseline violation is therefore not
uniformly detectable at runtime, which is an argument for keeping the declared
baseline honest rather than for adding dispatch.

### 1.2 The portable vector *type* is the expensive design, and the payoff doesn't need it

Atlas puts a first-class portable vector type at the very bottom of the stack —
`Vec32<u8>`, `Mask`, load/store/gather/scatter/shuffle/blend/compress/expand —
and forbids algorithms from touching AVX2 directly. The *discipline* is
correct. The *shape* is the expensive one for Fern, and the reason is
structural rather than a matter of effort.

Both native backends are **stack-machine code generators over 8-byte operand
slots**. Values are pushed and popped through GPRs; the vector register file
is entered and left inside a single op:

- `internal/codegen/x86_64/x86_64.go:1989` — f64 operands "move into xmm0 /
  xmm1 via `movd` (32) or `movq` (64)", compute, and move straight back
  (`movq rax, xmm1`).
- `internal/codegen/arm64/arm64.go:10095` — the bit patterns "are stored as
  i32 on the operand stack to keep the push/pop discipline uniform across i32
  / f32 / i64 / f64; **the V-register file gets involved only at op time**."
- `internal/codegen/x86_64/x86_64.go:2290` — "Slot i sits at `[rbp -
  (i+1)*8]`… Always 8-byte."

This is not merely how the comments read; it is what the backend emits. For
`var c: f64 = a * b + a`, `fern -target x86-64` produces:

```
    movabs $0x400c000000000000,%rax   ; f64 bit pattern in a GPR
    sub    $0x10,%rsp
    mov    %rax,(%rsp)                ; ... spilled to an 8-byte operand slot
    ...
    movq   %rcx,%xmm0                 ; enter the vector file
    movq   %rax,%xmm1
    mulsd  %xmm0,%xmm1                ; one op
    movq   %xmm1,%rax                 ; leave it immediately
    ...
    movq   %rcx,%xmm0                 ; re-enter for the next op
    movq   %rax,%xmm1
    addsd  %xmm0,%xmm1
    movq   %xmm1,%rax
```

No xmm value survives across an op boundary — each op re-enters and re-exits
the vector register file. That is exactly the fused shape this section
proposes for SIMD, already load-bearing in production codegen.

A 128-bit value does not fit an 8-byte slot. Making `Vec16<u8>` a first-class
IR value therefore means, in every one of **six** backends (three native, three
self-host): a second register class, vector-aware allocation and spilling, a
wider operand slot or a parallel vector stack, ABI rules for passing and
returning vectors, and a new type in the checker, the interpreter, and the
monomorphiser. That is the project the blueprint survey already sized
correctly when it said the vector surface "should be evaluated as one project
with that whole tier as its payoff, not attempted piecemeal"
(`docs/SOTA-STDLIB-BLUEPRINT.md` ▸ Tier 3).

But the payoff list does not actually require vectors to be *values*. Every
item on it is a **whole-loop kernel with scalar inputs and a scalar result**:

| Kernel | Signature |
| --- | --- |
| `memchr` | `(ptr, len, byte) -> index` |
| `memcmp` / `str_eq` | `(a, b, len) -> ordering` |
| UTF-8 validate | `(ptr, len) -> bool` |
| ASCII case / classify | `(ptr, len, out) -> ()` |
| SwissTable group probe | `(ctrl_ptr, h2) -> match_mask` |
| JSON structural classify | `(ptr, len, out_index) -> count` |

In each, the vector never crosses an IR value boundary. It is born from a
load, consumed by a compare, and reduced to a scalar before the op returns.
That is *precisely* the shape the existing f64 lowering already has — and it
means the whole tier is reachable with **no new register class, no regalloc
change, no type-system change, and no ABI change**.

Call this the **fused-intrinsic** design. It is specified in §3.

The honest cost of choosing it: portability discipline moves from the type
system to code review. Atlas's rule — "no algorithm in the standard library is
allowed to directly use AVX2" — is enforced by construction when there is a
`simd::Vec32<u8>` to write instead. With fused intrinsics, each kernel is
hand-written once per backend, so the SSE and NEON and wasm versions of
`memchr` are three separate pieces of code that must agree. §3 addresses that
with a mandatory scalar reference and differential testing rather than by
pretending the cost is not there. This is a real trade, taken deliberately:
six backends × a register-class project is a larger and riskier duplication
than six backends × a dozen leaf kernels, and the fused design leaves the
first-class vector type available later as a *widening*, not a rewrite.

### 1.3 Phase 1's allocator family is already answered — and one part of it was deliberately removed

Atlas Phase 1 is Arena + BumpAllocator + PoolAllocator + GeneralAllocator, with
"Compiler / Parser / JSON / Regex all use arenas." Fern's memory model is not
the malloc-plus-arenas world this assumes:

- a **16 GiB `MAP_NORESERVE` bump arena** (single cursor, both native backends),
- a **large-tier freelist** on top of it,
- **Perceus reference counting** with constructor reuse, in both the native and
  self-hosted compilers.

More pointedly: Fern **had** the user-facing arena Atlas is asking for — an
`arena { … }` block, `arena_save` / `arena_restore` builtins, per-request
bracketing in `tcp_serve` and the wasm `__http_entry` — and **removed all of
it** on 2026-06-01 (`docs/ARENA-DECISION.md`). Not because it was unfinished,
but because RC subsumed it: the two-cursor allocator underneath was
subsequently collapsed to one cursor because nothing selected the second
region any more.

The removal was not permanent in every form, and the shape that came back is
instructive about what Fern actually wants from an arena. A **one-level
checkpoint** — `__heap_mark` / `__heap_release_to`, which rewinds
`__fern_heap_ptr` and snapshots the freelist heads into a `.bss` shadow —
exists today, native-only, gated behind an `arena` capability in the platform
descriptors (`internal/platforms/enforce.go:47`, `platforms.go:85`). It is
native-only because wasm's linear-memory allocator has no room for the shadow
below its head table, and gating it turns an internal "unknown callee
`__fern_heap_mark`" mid-build failure into an E066 at check time.

So the accurate statement is not "Fern rejected arenas" but "Fern rejected the
*general* arena and kept a narrow checkpoint where it paid for itself". That
is the same adaptive-dispatch instinct the blueprint argues for, applied to
memory: the general mechanism lost to RC, and the specialised one survived.

The removal was eyes-open and was recorded as carrying a regression: RC cannot
collect cycles, so a long-running server would leak request-local cycles the
arena reset used to reclaim. **That regression did not materialise, and this
paragraph asserted it as live for one revision of this document.** Cycles are
not constructible in Fern — E048 (fields immutable after construction), its
subscript counterpart, and E057 (`Cell[T]` restricted to scalars and `string`
so a cell cannot close a cycle) between them reject every route
`CYCLE-COLLECTION-ANALYSIS.md`'s proof depends on. See §4's closed-questions
list for the verification.

So Phase 1's successor is not a cycle collector. It is ownership, which Atlas
does not address at all and which is where Fern's actual memory bugs are: the
seven unbounded self-host-vs-native reclaim leaks measured under
`FERN_LEAKCHECK=1` in **#6127** (~108 KB over four shapes remaining as of
`f58ab5d`).

**Verdict:** Phase 1 is closed as written, and so is the cycle question. The
successor item is closing #6127 — *reclaim*, not *allocation*.

The one Phase 1 idea that does survive intact is **small-object optimisation**
("every collection has inline storage"). Fern has it for strings
(`docs/SSO-PLAN.md`, `SSO-TWOWORD-FLIP-STATUS.md`) and has the analogous
`Map` win already (linear scan at or below 8 entries). Inline storage for small
arrays is a genuine open row.

### 1.4 Phases 7 and 8 are blocked on a memory-model decision, not on implementation

Atlas Phase 7 asks for Mutex, RwLock, Semaphore, Barrier, Channel, SPSC/MPSC/
MPMC queues, and a work-stealing executor; Phase 8 for io_uring / IOCP /
kqueue.

Fern has **no parallelism at all**, and the reason is not that nobody has
written a mutex. It is a hard constraint from the memory model, stated in
`docs/MULTICORE-RESEARCH.md`: **Perceus refcounts are non-atomic and must never
be touched from two threads.** Every primitive on Atlas's Phase 7 list
presupposes a shared mutable object graph, which is exactly what non-atomic RC
forbids. Shipping `Mutex<T>` into that model would not be a fast path with a
correct fallback; it would be a data race with a nice API.

The repo has already picked the compatible shape — share-nothing workers with
per-worker heaps (#5366) — and it makes most of Phase 7 moot: with no shared
graph there is nothing for an RwLock to guard, and the queue tier reduces to a
message channel between heaps. Concurrency proper (overlapping I/O on one
thread) is *done*: colorless `Future[T]` with `gather` / `race` /
`with_deadline` (`docs/ASYNC.md`, `std/async`).

Phase 8 inherits the same gating. io_uring's value is submitting many
operations without a syscall per operation, which is a throughput story for a
server saturating cores; on the single-threaded poll loop Fern runs today the
win over the existing readiness path is small, and the completion model would
have to be rebuilt when the parallelism shape lands. Sequenced after #5366,
not before.

**Verdict:** neither phase is a build item until the multicore shape is
decided. Until then the guardrail from `MULTICORE-RESEARCH.md` applies —
platform decisions should stop accreting against an *implicit* single-thread
assumption, which is a constraint on how new stdlib surface is written, not a
licence to build locks.

### 1.5 Phase 12 is already true, and it is why the string/map work ranks so high

Atlas Phase 12 — "the compiler should reuse the standard library: Arena,
BitSet, SparseSet, Interner, SmallVec, HashMap, Rope, PieceTable; nothing
compiler-specific unless absolutely necessary" — is Fern's existing position,
arrived at from the other direction. The self-hosted compiler is written in
Fern and is the heaviest consumer of `std/string` and `core/map` that exists.
There is no separate compiler-internal collection layer to unify.

The consequence Atlas draws is worth keeping, though, because it explains a
ranking that otherwise looks odd: substring search ranked *first* in the last
stdlib pass ahead of several algorithmically flashier rows, because it is on
the compiler's own hot path. That is the "one optimization unit" thesis paying
out, and it is the correct tiebreaker for future ranking too.

---

## 2. What Atlas contributes that Fern is genuinely missing

Stripping out what is done, blocked, or inverted leaves a short list — and it
is worth being clear that this is the document's actual output. In descending
order of value:

1. **Performance regressions must fail CI.** Fern tests correctness
   exhaustively (differential testing against the `-interp` oracle, small-
   alphabet enumeration — 51,396 cases for the search core) but **nothing gates
   performance or allocation volume**, which `docs/TEST-GATES.md` names
   explicitly as a hole. Every optimisation in the last stdlib pass is
   currently protected only by the fact that someone measured it once.

   Fern has the right instrument already and it is not the obvious one. Peak
   RSS is *not* comparable across hosts here: the arena is a 16 GiB
   `MAP_NORESERVE` mapping, so a first touch maps a 2 MB huge page under
   `THP=always` and a 4 KB page under `madvise` — the same binary on the same
   input measured **43 MB locally and 552 MB on CI**, a 12× spread with
   identical allocation. `__heap_bump_bytes()` (i64, exact, host-independent,
   meaningful under qemu) is the gate that works, joined by
   `__arr_push_shared_bytes()` for the rc==1 append cliff — and note that the
   *weighted* form is the one that ranks correctly: a whole-module compile
   crosses the cliff 188 times copying 812 bytes (noise) while one threaded
   accumulator copies 2.3 GB. Two rounds of past optimisation work were scoped
   against the unweighted count and aimed at sites that could not have paid.

   This is buildable now, on existing instruments, and it protects everything
   else on this list. **It should go first.**

   **First slice landed: `TestX86_64AllocScaling`** — and building it corrected
   the design sketched here. The obvious gate is a recorded byte budget per
   shape, which is what "record and compare" implies; that gate rots, because
   every legitimate change to a header size, growth schedule or SSO threshold
   moves every budget at once, so they get re-recorded in bulk without being
   read. Measuring the same shape at `n` and `2n` and bounding the **ratio**
   removes the problem: constant factors cancel, and the asymptotic class —
   the thing that actually turns a compile from seconds into minutes — comes
   through unmistakably. Measured: linear shapes 2.00–2.06x per doubling,
   quadratic 3.78x, against a 2.20x bound. See `docs/TEST-GATES.md`.

2. **The 128-bit SIMD tier** (§3), which unblocks the whole of
   `SOTA-STDLIB-BLUEPRINT` Tier 3.

3. **`byteswap` / `rotate` intrinsics** — the two remaining Phase 2 rows, the
   same `bitCountBuiltin` shape as the landed `clz`/`ctz`/`popcount`
   (`internal/ir/ir.go:18190`). The rest of Phase 2 is already shipped:
   `popcount`/`clz`/`ctz` are real intrinsics on every backend, and the
   overflow/saturating rows are a *decided semantics* rather than a gap —
   wrapping is the default with no trap, and `+|` `-|` `*|` `<<|` are the
   saturating operators (`docs/INTEGER-SEMANTICS.md`).

4. **Streaming compression** (Phase 9) — Fern has none, and Atlas's framing
   ("streaming only; never require loading an entire file") is the right
   constraint to adopt up front rather than retrofit.

5. **Crypto coverage** (Phase 10) — `std/crypto` has SHA-256, HMAC, PBKDF2,
   HKDF, HOTP/TOTP, and a constant-time compare. It has no AEAD (AES-GCM,
   ChaCha20-Poly1305), no signatures (Ed25519, X25519), no BLAKE3, no SHA-3.
   The AEAD gap is the one that blocks real protocol work. Atlas's "automatic
   hardware dispatch" for AES-NI runs into §1.1 — AES-NI is outside the
   declared x86-64 baseline — so ChaCha20-Poly1305 (fast in software, no
   hardware dependency, constant-time by construction) is the better first
   AEAD for Fern specifically.

6. **Big integers** (Phase 11) — no arbitrary-precision type exists. Atlas's
   threshold ladder (schoolbook → Karatsuba → Toom-Cook → NTT) is the correct
   design *when* the type exists; the prior question is whether Fern wants one.

Deliberately **not** on this list, having been checked: adaptive dispatch by
size/type/shape (already the blueprint's organising principle and implemented
in `cmp.sort`, `core/map`, `__str_find_from`, `parse_float`), Dragonbox,
Eisel–Lemire, Two-Way search, SwissTable-style small-map linear scan, seeded
hashing, and the bit-count intrinsics — all landed.

---

## 2b. Rows that have no referent in Fern

Separate from the five inversions, which are real ideas in the wrong order,
these are rows that do not describe anything Fern has or targets. They are
recorded so that a future reader does not spend time re-deriving why they were
dropped, and because the *pattern* is the useful part: each one encodes a
platform assumption a context-free list cannot know is false here.

| Row | Why it has no referent |
| --- | --- |
| **Windows / IOCP** (Phase 8) | Fern has no Windows target. `fern -targets`: arm64 Linux, arm64-android, arm64-darwin, x86-64 Linux, wasm, wasi-http. |
| ~~**macOS / kqueue** (Phase 8)~~ | **This row was wrong — see §2c.** It read "there is no macOS server story"; that was a statement about intent, not about the code, and the intent was mine rather than the project's. kqueue is a *deferred port with live TODOs*, not an unreferenced idea. |
| **`Vec64<u8>`** (Phase 0) | 512-bit vectors. AVX-512 is far outside the declared Haswell baseline; nothing Fern targets guarantees it. |
| **Gather / Scatter** in the portable SIMD API (Phase 0) | NEON has no gather and wasm `v128` has no gather. An abstraction containing them is not portable to two of three targets — it is an x86 API with fallbacks. |
| **Rope / PieceTable** as compiler infrastructure (Phase 12) | Zero occurrences in the codebase. These are *editor buffer* structures, not compiler ones; the list has them under the wrong heading. |
| **Big-integer NTT tier** (Phase 11) | There is no arbitrary-precision type at all. The 2048+-limb tier is computer-algebra territory, several decisions past "should Fern have bigints". |
| **Differential testing against glibc / LLVM libc / Rust / Go / Java** | Fern's differential oracles already exist and are stronger for its actual failure modes: `-interp` versus each compiled backend, and native versus self-host across ~1000 (fixture, target) pairs. Checking `sort` against Java's would find nothing those miss. |
| **"Cache misses / branch misses / SIMD utilization" as per-commit CI metrics** | Hardware perf counters are unavailable in most CI containers and meaningless under `qemu-aarch64`, which is how arm64 is tested. Unimplementable as stated — §2.1 is the version of this idea that works. |
| **`String<23>`** (Phase 1) | 23 is libstdc++'s inline capacity, a consequence of *its* three-word layout. Fern shipped a two-word string ABI (`docs/SSO-PLAN.md`), so the inline capacity follows from that decision rather than being free to pick. |
| **`Map<4>`** (Phase 1) | Fern's small-map linear scan cuts over at **8** entries, which is both already shipped and the better number. |
| **"Compiler / Parser / JSON / Regex all use arenas"** (Phase 1) | Fern's compiler, parser, and `std/json` are RC-managed; none of them bracket work in an arena (§1.3). |

The common thread: every row above is correct advice for a language with a
malloc heap, shared-memory threads, a Windows port, and x86 as the reference
architecture. Fern has none of those. This is the failure mode to expect from
any context-free "SOTA checklist" — not that the items are wrong in general,
but that the assumptions they are conditioned on go unstated, so the reader
cannot tell which ones transfer.

## 2c. macOS as a server target — decided in, and what it costs

**Decision (2026-08-06): Fern wants a macOS server story.** This section
replaces the §2b row that dismissed one, and the correction is worth keeping
visible because of *how* that row was wrong. It asserted "there is no macOS
server story" from the observation that `arm64-darwin` exists to produce Mac
binaries — a claim about intent dressed up as a claim about the code. The code
says something different and more useful.

**What already works on `arm64-darwin`.** More than the row implied. The
platform descriptor advertises `tcp` (`fern -targets`), and the socket
syscalls are genuinely ported — `socket` / `bind` / `listen` / `accept` all
carry Darwin numbers alongside their Linux ones in the arm64 backend's dual
syscall table. A blocking HTTP/1.1 accept loop — plain `tcp_serve` — is
therefore already a working macOS server.

**What does not work, exactly.** One thing: the readiness multiplexer.
`__fern_poll` is `ppoll(2)` on Linux and a **`-1` stub on Darwin**, with the
deferral recorded at three sites in `internal/codegen/arm64/arm64.go` (the
`linuxOnlySysno` note, `emitPollRuntime`, and the `usesPoll` field comment).
A `-1` return means "nothing is ready", so everything layered on readiness
degrades rather than erroring:

| Surface | On `arm64-darwin` today |
| --- | --- |
| `tcp_serve` (blocking accept loop) | works |
| `tcp_serve_deadline` | broken — the wait returns immediately, so the deadline fires at once |
| `std/async` `gather` / `race` / `with_deadline` | broken — all route through the `poll` builtin |

So the gap is not "macOS is not a server platform"; it is **one runtime helper
away from being one**. Porting `__fern_poll` to `kevent(2)` mirrors the
existing ppoll implementation: build the event set from the length-prefixed
`i32[]` of fds, request read-readiness on each, translate the millisecond
timeout into a `timespec` (negative meaning block), and return the index of
the first ready fd or -1. The signature, the caller contract, and the tests
are all already fixed by the Linux side.

Two things make this cheaper than it looks, and one makes it harder. Cheaper:
the whole async surface is *already* written against the `poll` builtin rather
than against `ppoll` directly, so nothing above the helper changes; and
`arm64-darwin` is already verified end-to-end on a `macos-latest` CI runner,
so there is a place to run it. Harder: kqueue is not a poll clone — it is
stateful (a kqueue fd holding registrations) where `ppoll` is stateless
(a fd set per call), so a faithful port either re-registers every call (simple,
correct, and the right first version) or keeps a persistent kqueue and grows a
lifetime the current helper has no place to store.

Sequenced as item 3 in §4. It is independent of the SIMD tier and of the
multicore decision — it is a per-target runtime port, not a language change.

## 3. The fused-intrinsic SIMD ABI

This is the concrete form of §1.2 and the first buildable slice of the SIMD
tier. It is deliberately specified as a *contract* rather than a set of
functions, so that adding the second kernel is mechanical.

### 3.1 The contract

A **SIMD kernel** is a single IR op that:

1. takes only scalar operands from the 8-byte operand stack (pointers as
   `WidthPtr`, lengths and bytes as i32/i64);
2. produces a single scalar result pushed back onto the operand stack;
3. contains its entire vector lifetime **within its own emitted instruction
   sequence** — no vector value is live across an op boundary, a call, a
   branch out of the kernel, or a spill;
4. uses only instructions inside the target's **declared baseline** (§1.1);
5. is byte-for-byte equivalent to a mandatory scalar reference implementation.

Rules 1–3 are what buy the "no register class" property, and they are what a
reviewer checks. Rule 4 is what makes dispatch unnecessary. Rule 5 is what
replaces the type system's portability guarantee.

### 3.2 Surface

Kernels enter the language the same way the bit-count intrinsics do — as
`__`-prefixed compiler builtins that a readable stdlib function wraps, never as
surface syntax users are asked to write (`internal/ir/ir.go:18190`, and the
`std/i32.count_ones()` wrapper pattern). The threading is: a name in the
builtin table → an `OpKind` → a lowering in each backend → an interpreter
implementation → a stdlib wrapper → tests.

### 3.3 First kernel: `__memchr(ptr, len, byte) -> i32`

Chosen first because it has the largest blast radius for the least code.
`std/string`'s search core `__str_find_from` already dispatches single-byte
needles to "the memchr shape" (`internal/stdlib/std/string.fern:269`), and
`contains` / `index_of` / `split` / `splitn` / `split_once` / `partition` /
`find_all` / `count` / `count_matches` / `replace` / `replace_n` / `replacen`
all route through that core — so one kernel lifts the entire forward-search
family plus the compiler's own lexing. Its backward
sibling `__str_rfind_from` gets the same treatment second.

Lowering sketch, all inside the declared baselines:

| Backend | Sequence (16 bytes/iteration) |
| --- | --- |
| x86-64 | `movd`+`pshufd` splat → `movdqu` / `pcmpeqb` / `pmovmskb` / `bsf` |
| arm64 | `dup` splat → `ld1` / `cmeq` / `shrn` (`.8b`, #4) / `fmov` / `rbit`+`clz` |
| wasm | `i8x16.splat` → `v128.load` / `i8x16.eq` / `i8x16.bitmask` / `i32.ctz` |

Tail bytes below one vector width run the scalar reference; correctness for
unaligned heads is handled by scalar-stepping to alignment rather than by
masked loads, which keeps all three sequences within baseline.

### 3.3a What the first kernel cost that this section did not predict

`__memchr` landed and immediately falsified a claim above. §1.2 and §3 argue
the fused design needs **no vector register class, no regalloc change, no
type-system change, and no ABI change**. All four hold. But they were written
as if that were the whole prerequisite, and it is not:

> **The in-process assemblers must be able to ENCODE vector instructions, and
> they could not.**

`internal/native/x86_64` — which `-target x86-64` uses by default, `-cc` being
the opt-out — had no vector surface whatsoever. Only the scalar float ops the
code generator uses to shuttle f64 through xmm. Not `movdqu`, not `pcmpeqb`,
not `pmovmskb`, not even `bsf`. The first SSE2 kernel therefore assembled
under GNU `as` and was rejected in-process, so it shipped scalar and was
vectorised in a second pass once the encodings existed.

That is a much smaller prerequisite than a register class — eighteen
encodings, pinned byte-for-byte against GNU `as` in `TestEncodeVectorSurface`
— but it is not zero, and it is per-assembler.

**It was per-assembler in the strongest sense: all three had the gap.** arm64's
`internal/native/arm64` had no NEON (it gained `DUP`/`LD1`/`CMEQ`/`SHRN`/`UMOV`,
verified against `aarch64-linux-gnu-as`), and `internal/wasm` had no v128 at
all — not one 0xFD-prefixed opcode, because nothing in Fern had ever asked for
one. Each needed its own prerequisite PR with its own external oracle. So the
rule to carry forward is not "check the assembler once" but **check the
assembler for every target you are about to emit a vector kernel on, and land
the encodings first** — which the arm64 and wasm kernels did, and the x86-64
one did not.

The wasm gap also has a sharper failure mode than the native two, worth
recording because it shaped that PR's tests. Vector sub-opcodes are uleb128,
and the space is dense: encode `i16x8.bitmask` (132) as a raw byte and you get
`f32x4.pmin` — a *valid* instruction. The module still validates and still
runs. Nothing short of an external assembler can tell you which instruction you
actually emitted, which is why `internal/wasm/simd` is pinned against
`wasm-tools` and not against a table someone typed.

The lesson generalises past this row: "no IR change" is not the same as "no
toolchain change", and the fused design's whole argument is that it stays
below the IR. Below the IR is where the assembler lives.

### 3.4 Ordering across six backends

The kernel must exist in all six backends before `std/string` may call it,
because the self-hosted compiler compiles the stdlib and a missing lowering is
a hard compile error, not a fallback — the AST emitters are gone and every
backend routes IR-or-error (`docs/SELFHOST-AST-RETIREMENT.md`). The sequence
is therefore:

1. IR op + interpreter reference + scalar-only lowering in all six backends
   (correct, not yet fast) + differential tests.
2. `std/string` adoption behind the now-total intrinsic.
3. Vectorise the lowerings one backend at a time, each with a measurement.

Step 1 is what makes steps 2 and 3 safe: the intrinsic becomes total *before*
anything depends on it being fast, so no step in the sequence can leave the
tree unbuildable.

**Status.** The three *native* backends are done, and each was vectorised as it
landed rather than in a separate pass — the ordering above splits steps 1 and 3
to protect the tree, and once the assembler prerequisite of §3.3a is paid for a
target there is nothing left to protect it from. x86-64 is SSE2 (`pcmpeqb` /
`pmovmskb` / `bsf`, ~12x), arm64 is NEON (`cmeq` / `shrn` / `rbit`+`clz`), wasm
is v128 (`i8x16.eq` / `i8x16.bitmask` / `i32.ctz`). The wasm one carries a
second, scalar path the natives do not need: a short wasm string lives in its
two words with no address, so there is nothing for `v128.load` to point at.

The three *self-host* backends followed, and there step 1's scalar-first
ordering is the whole point rather than a formality: `asm_ir.fern` /
`asm_arm64_ir.fern` emit an inline byte loop, `wasm_ir.fern` a
`$__fern_memchr` WAT helper. Step 1 is therefore **complete — the intrinsic is
total**, and step 2 (`std/string` adoption) is unblocked.

One thing the six lowerings did *not* end up sharing is a string
representation, and it is worth recording because it is what made three of the
six non-trivial. Native x86-64 uses a one-word `string`; native arm64 and
native wasm use two words with small-string optimisation (and wasm's SSO form
has no address at all, so its kernel needs a second scalar path); the self-host
backends use a `[data@0, len@8]` box on the register targets and a
`[len@0][bytes@4]` block on wasm. The op is the same op in all six; nothing
below it is.

Remaining: step 3 for the self-host tier — swapping those scalar bodies for
vector ones. Unlike the native tier that is pure speed with no totality
argument behind it, so it ranks below §4's other items.

### 3.5 Testing

Per rule 5, each kernel ships with:

- an exhaustive differential test against its scalar reference over a small
  alphabet — the shape that found the Two-Way bugs example-based tests missed,
  covering every length across the vector-width boundary (0..2W+2) and every
  alignment offset;
- runs on interp, x86-64, arm64, and wasm, plus through the self-host compiler
  on wasm and x86-64;
- a `__heap_bump_bytes()` assertion that the kernel allocates nothing;
- once item 1 of §2 exists, a throughput gate.

---

## 4. Sequenced plan

1. **Performance-regression CI** on `__heap_bump_bytes()` /
   `__arr_push_shared_bytes()` — protects everything downstream. (§2.1)
   *Slice 1 landed (`TestX86_64AllocScaling`, allocation asymptotics).
   Remaining: extending the corpus to the compiler's own hot shapes, and a
   throughput gate.*
2. **`__memchr` as the first fused kernel**, by the §3.4 ordering. (§3.3)
3. **`__fern_poll` on kqueue for `arm64-darwin`** (§2c) — the one item here
   that turns a target from half-working into whole. Independent of everything
   else on this list: a per-target runtime port, not a language change, and
   the caller contract is already pinned by the Linux side.
4. **`__memcmp` / UTF-8 validation** as the second and third kernels, proving
   the contract generalises past a single shape.
5. **`byteswap` / `rotate` intrinsics** — small, independent, unblocked. (§2.3)
6. **SwissTable SWAR group probe** for `core/map` — the one Tier-3 item with a
   credible non-vector variant, so it can proceed in parallel.
7. Re-evaluate the first-class vector type only after 2 and 4 have landed, with
   real kernels to point at. If the fused set stays under a dozen leaf kernels,
   the register-class project may never be worth it.

Two of the open questions this document opened are now **closed**, both by the
same route — someone decided, and the decision turned a "needs a decision" row
into an ordinary build item:

- **Does Fern want a bigint type?** Yes (2026-08-06). `core/bigint` shipped:
  sign-magnitude, base 2^32 limbs in u64 slots, schoolbook multiply only, with
  its trait impls in `core/cmp` to keep the module import-free. The Karatsuba /
  Toom-Cook / NTT ladder stays unbuilt until there is a workload to measure.
- **Is macOS a server target?** Yes (2026-08-06) — §2c, sequenced as item 3.
- **How should Fern collect reference cycles?** **It should not — cycles are
  not constructible.** This question was listed here as open, and §1.3 asserted
  that a long-running server "leaks any reference cycle a handler builds". Both
  were wrong, and wrong by trusting a stale document rather than the checker:
  `CYCLE-COLLECTION-ANALYSIS.md`'s TL;DR still says a cycle is constructible,
  which was true on 2026-06-01 and has not been since. Its own recommendation —
  cycle-free by construction, enforced by the checker — is what shipped. **E048**
  makes struct fields immutable after construction (killing `a.next = [b]`, the
  only mechanism that doc's proof relies on), its subscript counterpart does the
  same for elements, and **E057** restricts `Cell[T]` to scalars and `string`
  *explicitly* so a cell cannot reconstruct a cycle
  (`internal/checker/checker.go:635`). Verified 2026-08-06: the proof program
  now fails `-check`; `Cell[Node]` and `Cell[fn]` are rejected; a struct rebuild
  captures a snapshot rather than a back-edge.

  The one thing that keeps this live: a future feature reintroducing interior
  mutability over composite types — `Cell[T]` widened past scalars, a `ref` or
  `weak` type, or field assignment coming back — reopens it. Such a feature
  should cite `CYCLE-COLLECTION-ANALYSIS.md` and this entry.

Still open, and still decisions rather than implementations:

- **The multicore shape** (§1.4). #5366's share-nothing workers with
  per-worker heaps is the candidate, forced by non-atomic Perceus refcounts.
  Until it is settled, new stdlib surface should stop accreting against an
  implicit single-thread assumption.
- **The transcendental accuracy contract.** wasm polynomial approximations and
  native libm disagree *today*, so `sin(x)` is already backend-dependent. Fern
  should decide whether it promises correct rounding, a stated ULP bound, or
  "whatever the platform does" — and if it promises anything, the wasm path is
  where the promise breaks first (`SOTA-STDLIB-BLUEPRINT`).

---

## 5. On the Atlas Principles

The ten principles hold up essentially unchanged, and are worth adopting as
written with two amendments:

- **"SIMD by default"** should read *"vectorise where the baseline guarantees
  it"*. The unqualified form invites the dispatcher §1.1 argues against, and
  invites reaching for AVX2 outside the declared baseline where it fails
  silently.
- **"Allocation is explicit — prefer stack, inline storage, arenas, then the
  general heap"** does not describe Fern and should read *"prefer inline
  storage, then reuse, then fresh allocation"*. Fern has no general heap tier
  to fall back to, and its arena tier is one native-only checkpoint rather than
  a user-facing allocator (§1.3); the ladder that matters here is Perceus
  constructor reuse, and the measurable form of the principle is
  `__heap_bump_bytes()`.

The most valuable of the ten for this codebase is **"measure before
optimizing"**, for a reason specific to Fern: three attributions in #6127 were
wrong because a sub-shape went unprobed, two rounds of `own`-conversion work
were scoped against an unweighted counter that could not have ranked the
sites, and a 100 MB RSS ceiling failed a change that had just made the code 50×
leaner. The instrument matters as much as the discipline.
