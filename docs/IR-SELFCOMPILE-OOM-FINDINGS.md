# Self-host IR self-compile OOM — root-cause findings

Date: 2026-06-20.
Status: investigation complete; fixes scoped, not yet implemented.

> **CORRECTION (2026-06-21) — see "Finding 2 RE-MEASURED" below.** Direct
> measurement with `cmd/fern`-compiled microbenchmarks contradicts this
> document's central Finding-2 claim that the 45-field `LowerState`
> rebuild *inherently* clones O(N²). It does **not**: clean self-reassign
> threading (`s = s.emit(x)`) is **in-place** even at 45 fields and 20
> parallel arrays (flat ~2 MB over 8000 iters). The clone fires only when
> a state is **read after being threaded** (kept alive, rc≥2). So the
> "move `ops`/`local_*` out of `LowerState`" rework (Plan A /
> `docs/EFFECT-A-LOWERSTATE-INPLACE-PLAN.md`) attacks a rebuild that is
> already in-place under clean threading, and is **not** the right fix.
> The real cost is a *linear* per-allocation leak (Effect B) plus a
> specific, localized keep-alive — see the re-measured section.

## Question that started this

> Would moving to a **flat AST** (one contiguous array of nodes with
> integer-index child links, à la V / Zig / Sorbet) reduce the memory
> used when compiling the self-hosted compiler — which currently OOMs
> when it routes through the IR code paths?

**Short answer: no.** A flat AST targets the wrong layer. The OOM is
not driven by AST-node pointer overhead; it is driven by (1) a known
reference-counting gap that never frees heap boxes and (2) an O(N²)
copy pattern in the IR lowering state. Neither is affected by how AST
nodes link to one another. This document records the evidence and the
two real fixes.

## How the OOM is gated today

`asm_ir.fern:eligible_core_known_main` caps IR routing at 512
functions:

```
if (mod.funcs.len() > 512) { return false; }
```

Above the cap a module falls back to the legacy AST→asm emitter. The
cap exists specifically so the merged ~1000-function whole-compiler
bundle does not march the bump heap past its 3.875 GiB ceiling
(`0xF8000000`) and exit-137. Lifting the cap is the goal; the two
findings below are what stand in the way.

The cap was added in `0503f5a` (2026-06-19 21:00), **three hours
after** the native large-tier freelist recycle fix `77229d1`
(2026-06-19 18:06). So the cap is *not* stale relative to that fix —
recycling the freelist was already in place and was not sufficient,
because the dec path never feeds the freelist (Finding 1).

## Empirical measurement

Method: build the `asm_run.fern` driver with `cmd/fern` (native
x86-64 runtime), temporarily raise the cap to 100000, feed generated
single-module programs of pure-i32 functions (IR-eligible) on stdin,
and record peak RSS via `getrusage(RUSAGE_CHILDREN)`.

### Effect A — super-linear cost *within a single function*

| functions × statements | peak RSS |
| --- | --- |
| 1 × 100 | 46 MB |
| 1 × 200 | 72 MB |
| 1 × 400 | 167 MB |
| 1 × 800 | 488 MB |
| 50 × 1000 | **exit 137 (OOM at 3.875 GiB)** |

Doubling a single function's body roughly *triples* memory — ≈ O(N²)
in statement count. One ~1500-statement function would OOM on its own.

### Effect B — linear in *total* statements across all functions

| functions × statements | total stmts | peak RSS |
| --- | --- | --- |
| 500 × 20 | 10 000 | 484 MB |
| 2000 × 20 | 40 000 | 2017 MB |

4× the functions → 4× the memory: ≈ 48 KB retained **per source
statement**. Peak tracks the **sum of every *unreclaimed string* ever
allocated — across all functions and all lowering passes — not the live
set** (struct/array storage *is* reclaimed; see the corrected Finding 1
below). That is the signature of a non-freeing allocator for one type
category (Finding 1), independent of the quadratic.

A flat AST changes neither table: both scale with *IR allocations
performed during lowering*, not with AST-node storage.

## Finding 1 — unreclaimed **strings** (CORRECTED 2026-06-20)

> **Correction.** An earlier revision of this doc blamed Effect B on the
> generic `__fern_rc_dec` never freeing **struct / enum / string** boxes.
> Direct measurement shows that is wrong for structs and enums: the
> codebase already generates per-type recursive drops
> (`__drop_struct_<N>` / `__drop_enum_<N>` via `dropFnNameFor` →
> `genStructDropFn`, dispatched in `internal/ir/ir.go:emitDec`) that
> free the box, and `ast.RcFreeEnabled` is `true` by default. The
> generic no-free `__fern_rc_dec` is only the **declined-type fallback**.
> Effect B is **strings**, which are genuinely unreclaimed.

Empirical isolation (each a tight `while` loop, peak RSS via
`getrusage`):

| workload | allocations | peak RSS |
| --- | --- | --- |
| `S { a, b }` struct per iter | 10,000,000 | **9 MB** (freed/recycled) |
| `grow("x",4)` heap strings per iter | ~2,000,000 | **76 MB** (leaked) |

Structs are reclaimed; strings are not. So the ~48 KB/statement of
Effect B is dominated by the compiler's **string** churn — every
`Op.kind` / `Op.str`, every mangled symbol / local name, every
intermediate of an emitted-asm concat. `__drop_struct_Op` frees the
`Op` box and `__fern_arr_dec` frees the `Op[]` buffer, but each Op's
`kind`/`str` **string fields leak**, and they vastly outnumber the
boxes.

Why strings leak while the rest frees: heap strings are allocated by
`__fern_strcat` (and the other string-producing runtimes) through
`__fern_alloc_rc1`, so they **already carry `rc=1` at `[data-8]` and
their payload size at `[data-4]`** — the same header closures use. The
two-word ABIs (wasm + arm64-`TwoWordOverride`) **already reclaim**
strings: they retain on alias (`__fern_str_inc`) and free on the last
drop (`__fern_str_dec`). The leak is **single-word x86-64 only**, where
the string drop routes to the generic `__fern_rc_dec`, which only
decrements — so heap strings hit rc=0 and are abandoned, never returned
to the freelist.

### Fix — incrementally route native string drops to a freeing `__fern_str_dec`

The single-word x86-64 string drop is a strict superset substitution: a
new `__fern_str_dec` (the `__fern_closure_drop` pattern — guards: null /
inline-SSO low-bit / below-`0x1000_0000` literal / high-bit sentinel;
then `rc==1 → __fern_box_free(data, [data-4])`, else defer to
`__fern_rc_dec`) replaces `__fern_rc_dec` at a native string drop site.
The freeing is **balanced** because native strings *are* retained on
alias — `needsRcIncOnAlias` returns true for `StringType` and
`emitAliasInc` falls through to `__fern_rc_inc` for the single-word case
(the two-word-gated `__fern_str_inc` is just the fat-pointer-specific
helper; native uses plain `rc_inc` at the same logical alias sites). The
dec was simply never freeing.

> An earlier revision of this section claimed native strings have **no**
> retain side and that the dec-only free is an unconditional UAF. That
> was a misread of `emitAliasInc` — native strings *do* `rc_inc` on
> alias. The drop-site substitution is sound where the alias-inc is
> actually emitted; it must still be done **per drop site**, validated
> against the inc that feeds it.

**Shipped slice — struct fields.** `genStructDropFn`'s native
single-word string field drop now emits `__fern_str_dec`. The field is
retained on construction (struct field-init `emitAliasInc → __fern_rc_inc`
when the initialiser aliases, or moved in when fresh-owned), so freeing
the buffer at the field's `rc==1` under `__drop_struct_<N>` is exactly
balanced. Measured: a `2M ×` struct-with-one-heap-string-field loop
**91 MB → 9 MB**. Validated by the `rc_correctness` corpus (new
`struct_string_field_aliased` case — shared buffer across two structs +
a live local, returns 0 = correct value **and** zero
`__rc_underflow_count`), by the freelist/recycle harness, and under
`RcFreeDebug` poison mode (the UAF detector traps on any stale access;
the aliasing stress stays clean). `TestLowerStringStructFieldReclaim
OnNative` pins the codegen.

**Measured self-compile impact: ~none (yet).** A 500×20 generated module
self-compiled by the `asm_run` driver is **1100 MB before and after**
this slice; a pure-i32 500×20 module is 662 MB, so strings account for
~438 MB — but that ~438 MB is **not** captured here. Two reasons, both
upstream of the struct-field drop: (1) the dominant string locals are
**bare `var s = …` locals**, which native does not drop at all yet (no
exit-sweep / reinit free — see "Remaining sites" below); and (2) the
compiler's own `Op.kind` / `Op.str` fields live in `Op` structs that are
**never dropped** in the self-compile — they sit in the cloned / threaded
`LowerState` op arrays that leak under Effect A (Finding 2), so
`__drop_struct_Op` is not reached. So this slice is a correct,
general-purpose win (any program that *drops* a string-bearing struct now
reclaims the buffer — `91 MB → 9 MB` above), but the **self-compile**
string win is gated on (a) the bare-local drop slice and (b) dropping the
`Op` containers (Effect A). The earlier "highest-value site for the
self-compile" framing was optimistic — corrected here against
measurement.

**Slice 2 (owned string temps + locals, #3611) — also ~no self-compile
impact.** Routing the owned/sole-owner native string drops to
`__fern_str_dec` (fresh call-arg temps `f(a + b)`, owned reinit/exit
locals) is a real general-purpose win — a 2M-iter `consume(a + "_suffix")`
loop goes from a per-iteration leak to **9 MB** — but the same 500×20
self-compile is **1099 MB** (was 1100). big500's *input* concats
constant-fold, so the ~438 MB of self-compile string memory is
**compiler-internal** (`Op.kind`/`Op.str`, mangled names, lexer tokens)
and trapped in the leaked Effect-A containers, which neither string slice
reaches.

**Effect A confirmed real (not a measurement artifact).** Re-measured
single-function programs **each in a fresh process** (so the
`getrusage(RUSAGE_CHILDREN)` cumulative-max can't inflate later runs):

| 1 function × N statements | peak RSS |
| --- | --- |
| 100 | 99 MB |
| 400 | 219 MB |
| 800 | 536 MB |
| 1600 | 1576 MB |

~2.4–2.9× per doubling — genuinely super-linear. (A by-value-function
array-threading analog — `s = emit(s, v)` with `s.a.append(v)`, 20k iters
— stays at **9 MB**, i.e. the clones *are* freed there; the self-host
blow-up is the 45-field `LowerState` rebuilt per emit plus the 17
`.with()`-cloned `local_*` arrays, Finding 2.)

**Net for the self-compile:** Effect A is the **sole lever** — the
string-reclamation slices (#3600, #3611) are correct and worthwhile for
general programs but do not move it. The Effect-A fixes are the O(M) ops
`OpsBuilder` (blocked by the self-host AST-backend union-field miscompile,
#3554 — unblock = roadmap goal #1) and the `local_*` collapse
(constant-factor, unblocked but a large `irlower.fern` refactor).

**Remaining native string drop sites** (each its own incremental,
poison-validated slice): the function-exit sweep / reinit drop of a bare
string local, `emitOwnedTempStackDrop` (fresh concat/slice temps,
provably sole-owner so trivially safe), and string **elements** of
arrays / tuples / enums / closure captures.

> **Note — bare locals need predicate widening, not just drop routing.**
> Unlike the struct field (which flows through the always-generated
> `__drop_struct_<N>`), a bare native string *local* is **never tracked
> for the exit sweep / reinit drop at all** — verified: even `function
> f(x: string): i32 { var s = x + "yy"; return s.len(); }` emits **no**
> string dec on native (`ptrW=8`). The eligibility gate
> (`computeFreeEligible` / `rcTracked`) doesn't admit native string
> locals, so the `b.ptrW == 8` arms in `emitDec` / `emitOwnedSlotDrop`
> are unreached. Routing those arms to `__fern_str_dec` is therefore dead
> code until the **tracking predicate** is first widened to native
> strings (the inc/dec-balance work — `needsRcIncOnAlias` is already true
> for strings, but the exit-sweep / reinit eligibility is not). That
> predicate widening — not the drop helper — is the real bare-local
> slice, and it must stay balanced against the alias inc (some
> specialized two-word `str_inc` sites — tuple element, `Map[K,string]`
> get/get_or — have **no** native `rc_inc` branch yet, so those element
> categories need the inc added first or they would over-release).

Scope: x86-64-only (arm64/wasm already reclaim strings via the two-word
`str_inc`/`str_dec` pair), so each slice is fully **locally** testable.
Heap-corruption class — gate every slice on the x86-64 `rc_correctness`
corpus + freelist harness + a `RcFreeDebug` poison run. Tracks
`docs/RC-PERCEUS-PHASE-1E-PLAN.md`'s **Phase 1e-strings**.

### Bare-local slice attempted (2026-07-01) — RC-correct but NO RSS win; the blocker is allocator reuse, not eligibility

The "predicate widening, not just drop routing" note above is now **stale
in one respect**: `rcTracked` (`ir.go`, "All non-zero ptrW with strings is
rc-tracked") **already admits native single-word string locals**, and
`emitDec`'s `StringType` arm **already carries the native `b.ptrW == 8`
free branch** (`__fern_str_dec` — rc==1 `__fern_box_free`, else defer),
gated on exactly `eligible`. The only thing still gating the bare-local
free was `computeFreeEligible`'s `StringType` case, which set `elig` only
`if ast.UseTwoWordStrings(b.ptrW)`. So the slice reduces to **one line**:
drop that `if` so native string locals join `elig` (the loop already
skips tainted / uncounted-alias sources — `rhsTainted` taints every
`FieldAccess`/`Index`/`Call` RHS, and aliases are balanced by
`needsRcIncOnAlias → __fern_rc_inc`).

**That change was made and fully RC-validated** — the x86-64
`rc_correctness` corpus, `FixturesFreeMatchesNoFree`, `ReuseMatchesNoReuse`,
`FreelistReuse`, and the underflow detector all pass, and the emitted asm
gains the `__fern_str_dec` calls (baseline emits **zero** string decs for a
bare local — confirming the "never dropped" observation). **But it produced
NO peak-RSS reduction.** A 2M-iter `var s = base + "_payload…"` reinit-drop
loop stays at **~91 MB** (200k → 9 MB, 2M → 91 MB — linear, i.e. still
leaking) both before and after.

Root-caused by gdb: `__fern_str_dec` **does reach the free path** (rc==1,
the `< 0x10000000` literal-guard passes for the mmap heap at 0x10000000),
so the buffer **is** handed to `__fern_box_free` → `__fern_free`. The leak
is that the freed box is **not reused**: consecutive freed string data
pointers *grow* every iteration (`0x10000008, 0x38, 0x68, 0x98, …`) instead
of cycling between two addresses. So the allocator **bumps a fresh box
per iteration even with the freelist populated** — for the compiler's
(and this loop's) **alloc-new-then-free-old** ordering, `__fern_alloc`
isn't pulling the just-freed class back out. (The `FreelistReuse` corpus
passes because it checks reuse *correctness* on a hand-shaped
free-then-alloc, not the alloc-then-free-old steady state.)

**Redirect.** The native string leak's blocker is **allocator freelist
reuse**, NOT the RC eligibility gate. Widening `computeFreeEligible` is
correct and a prerequisite, but it is **dead weight until the allocator
reuses the freed class** — so it was **reverted** (no point shipping the
extra `__fern_str_dec` calls for zero benefit).

**ROOT CAUSE (2026-07-01, gdb-confirmed) — a size-class mismatch between
string alloc and string free.** `__fern_strcat` requests `la + lb + 1`
from `__fern_alloc_rc1` (`x86_64.go` ~4897 — the `+1` is a trailing NUL it
writes at `data+la+lb`, needed so the NUL fits when `len+8` lands exactly
on a 16-byte class boundary), but stores only the **length** at `[data-4]`.
`__fern_str_dec` (and `__fern_box_free`) frees using that stored length:
`box_free(data, len)` → `__fern_free(box, len+8)`. So the box is
**allocated** at `class(len+1+8)` but **freed** to `class(len+8)`. These
differ whenever `len ≡ 8 (mod 16)` (e.g. `len=24`: alloc `33`→48→**class
2**, free `32`→**class 1**). gdb on a `len=24` churn: every alloc requests
33 (→ class 2, whose freelist head stays `nil` → bump), while the freed
boxes pile up unused in the **class-1** head (`0x10000000, 0x30, 0x60, …`).
So `~1/16` of `strcat` results (the boundary-straddling lengths) are freed
into a bucket their own re-allocation never consults — a permanent leak,
no reuse, and **exactly** why every string-reclamation slice showed
"~no self-compile impact." (`__str_slice` is consistent — it allocs
exactly `new_len`, writes no NUL, and `str_dec`'s `len`-based free matches
it; the bug is strcat-specific, but any producer that over- or
under-allocates vs. the stored length has the same failure mode.)

**Fix options (each string-ABI, heap-corruption class — validate on the
x86-64 `rc_correctness` corpus + freelist harness + `RcFreeDebug` poison,
and mirror the audit on arm64/wasm two-word `str_dec`):**
1. *Make `str_dec` free `len+1`* — matches strcat, but is a UAF/overwrite
   hazard for any producer that allocs exactly `len` (e.g. `__str_slice`),
   so it requires first making **every** heap-string producer alloc
   `len+1`+NUL. Broadest, riskiest.
2. *Make `strcat` match `__str_slice`* (alloc `la+lb`, drop the NUL) — a
   single-site fix that would make free/alloc agree at `class(len+8)`.
   **BLOCKED (verified 2026-07-01):** `__fern_read_file` (and the other
   path syscalls) pass the string's data bytes **directly** to `openat`
   via `emitStrDataPtr` with no copy / self-termination, so a strcat'd
   path (`dir + "/" + file`) relies on strcat's trailing NUL. Dropping it
   would break `open`/`read_file` for concatenated paths. So the NUL must
   stay, and the alloc must keep the `+1`.
3. *Store the allocated payload size, not the length, in the free header* —
   cleanest in principle but the header slot is dual-purpose (`len()` reads
   `[data-4]`), so it needs a second word or a length-elsewhere scheme.

With option 2 blocked, **option 1 is the path**: make every heap-string
producer allocate `len+1`+NUL (audit `__str_slice`, `int_to_string`/radix,
`chr`, `str_repeat`, case/reverse, `read_line`/`env`/`args`/`read_file`,
strbuf-take), then flip `str_dec`/`box_free` to free `len+1`. Do it as one
atomic change (a half-converted set over-/under-frees → heap corruption),
gated on the full x86-64 RC corpus + freelist + poison, mirrored on the
two-word `str_dec` path (arm64/wasm may carry the same latent straddle).
This supersedes the "bare-local slice is the next lever" framing above:
the lever is the **alloc/free size-class agreement**, after which the
already-present `__fern_str_dec` + the (re-applied) `computeFreeEligible`
widening reclaim string locals for real.

### LANDED + measured (2026-07-01, #4174) — real reclaim, but ZERO self-compile impact

All three fixes shipped: (1) size-class agreement (`str_dec` frees
`length+1`, the four `+0` producers request `length+1`); (2)
`computeFreeEligible` admits native string locals; (3) the nested-concat
operand drop routes to the freeing `__fern_str_dec`, not the dec-only
`__fern_rc_dec`. **General-purpose win confirmed:** a `var s = base + "…"`
churn drops **91 MB → 128 KB**, and a nested `a + b + a + b` loop goes from a
multi-MB bump to bounded (freed *and* recycled, gdb-verified address reuse).

**But the self-compile is UNMOVED — this closes the string avenue for the
cap.** Direct measurement (cap raised to 100000, the `asm_run` driver
compiling a 500×20 module, peak RSS via `getrusage`):

| compiler | 500×20 peak RSS |
| --- | --- |
| pre-#4174 | 372 MB |
| post-#4174 | 370 MB |

i.e. **~0.6 %** — noise. The completed, fully-working string reclamation
does **not** reduce the self-compile, confirming the earlier partial-slice
observation with the finished fix. The reason is now definitive: the
self-compile's dominant strings are **`Op.kind` / `Op.str` fields living in
the persistent op-stream arrays**, never dropped as locals/temps (so
`__fern_str_dec` never sees them) — they are freed only when the `Op` array
itself is, which is the **Effect-A** clone/leak (Finding 2). So the cap lever
is the **op-array reclamation (Effect A)** exclusively; string-side
reclamation is done and should not be revisited for the cap.

### LANDED + measured (2026-07-01, #4187) — pointer-element `.with` UAF fixed, but ZERO self-compile impact

#4187 fixed a genuine use-after-free: `arr.with(i, v)` on an array of
single-word rc-tracked pointer elements (struct / enum / array / tuple /
closure) miscompiled on the copy-on-write **copy** branch — the plain
`__fern_arr_cow_inplace` `memcpy`'d the pointer elements without inc'ing
them, so the fresh copy shared the receiver's element boxes at unchanged
rc and dropping either array freed them under the other. The fix adds a
pointer-aware `__fern_arr_cow_inplace_ptr` (all three backends) that
retains each copied element, plus the overwritten-element drop and
aliased-value retain in `emitArraySet`. **General-purpose win confirmed:**
a functional-update `.with` churn on a struct-pointer-element array drops
from ~141 MB → ~2 MB peak (fully recycles), and the latent UAF is gone
(clean under `RcFreeDebug`).

This is exactly the shape `irlower.fern` threads —
`s.locals.with(i, li_set_X(s.locals[i], v))` on `locals: LocalInfo[]`
(a struct-pointer-element array, updated on the CoW copy branch of the
`LowerState { … }` functional update, ~20 `.with` per lowered function).
So it was the natural candidate for the Effect-A lever.

**But the self-compile is UNMOVED — byte-identical at every shape.**
Direct measurement (the `asm_run` driver compiling a generated module,
peak RSS via `getrusage(RUSAGE_CHILDREN)`, built from the SAME self-host
sources with the pre-#4187 vs post-#4187 native compiler — the driver
binaries genuinely differ, so the fix applied):

| workload | pre-#4187 | post-#4187 |
| --- | --- | --- |
| 100 fns × 20 stmts | 93 MB | 93 MB |
| 250 fns × 20 stmts | 180 MB | 180 MB |
| 500 fns × 20 stmts | 331 MB | 331 MB |
| 1 fn × 500 stmts | 137 MB | 137 MB |
| 1 fn × 1000 stmts | 364 MB | 364 MB |
| 1 fn × 2000 stmts | 1135 MB | 1135 MB |

Identical **to the kilobyte** in every row. Two things fall out:

1. **Small-function peak is LINEAR in function count** (persistent
   whole-module output: the accumulated emitted asm / op-stream for all
   functions, held to the end — not reclaimable, and not what the fix
   touched).
2. **Single-function peak is SUPER-LINEAR in statements** — 137 → 364 →
   1135 MB for 500 → 1000 → 2000 statements (~O(N²)). This *is* Effect A
   (Finding 2), and it is the shape the real 512-function bundle OOMs on
   (large compiler functions). The fix does **not** move it.

**Why the element-level fix can't move the Effect-A peak.** The O(N²)
is the `LowerState` **record / `locals` array** being cloned-and-leaked
per statement (Finding 2) — an ARRAY/RECORD-level lifecycle cost. #4187
corrected the rc bookkeeping of the *elements inside* a `.with` copy; it
does not change whether the *old locals array* (or the old `LowerState`
record) is freed each iteration. Those still leak under Effect A, so the
live set — and thus the peak — is unchanged. Like the string fix (#4174),
#4187 is a real correctness/reclamation win that does **not** reduce the
self-compile peak.

**Net: the cap lever remains Effect A exclusively, and specifically at
the record/array level** — either make the `LowerState` record reuse its
box in place across the functional update (blocked today: `LowerState`
has `string` fields, so it is `structReuseEligible = false` and the
self-overwrite / cross-reuse constructor-reuse paths bail), or restructure
the lowering so `locals` / `ops` are threaded as sole-owner *mutable*
locals rather than immutable record fields rebuilt via `.with` / `.append`
(so the per-statement clone disappears entirely). Element-level rc fixes
(#4174 strings, #4187 pointer elements) are done and should not be
revisited for the cap.

## Finding 2 — O(N²) `LowerState` rebuild in IR lowering

`examples/self_host/irlower.fern` threads all per-function lowering
state through one immutable record:

```
pub struct LowerState {   // ~45 fields, of which 17 are parallel
    ops: ir.Op[],         // local_* arrays indexed by local slot:
    local_names: string[],     local_is_str, local_is_strarr,
    local_is_arr: boolean[],   local_struct_type, local_opt_type,
    ...                        local_tuple_elems, local_map_type,
}                              local_is_i64/f64/f32/f64arr/i64arr,
                               local_is_closurearr/arrarr/u32/u64,
                               local_subword  (17 total)
```

Every state update reconstructs the **entire** record (24
`return LowerState { ... }` sites). The single-field helpers do, e.g.:

```
return LowerState { ops: s.ops,
                    local_names: s.local_names.with(i, name),
                    local_is_arr: s.local_is_arr, ... /* 40+ more */ };
```

Two costs compound, both per local introduced:

1. **`.with()` clones, it does not share.** In a struct-field-value
   position the receiver `s.local_names` is still referenced by `s`
   (rc ≥ 2), so `.with(i, name)` takes the **clone** path, *not* the
   in-place rc==1 fast path reserved for the `a = a.with(i,v)`
   self-reassign form (see `irlower.fern:4963`). Cloning an array of
   the current local count on every local add ⇒ O(N²) element copies
   over N locals.
2. **A fresh ~45-field box per update**, several updates per
   statement. With Finding 1 in force none of these intermediate
   boxes or arrays is ever freed, so the O(N²) copies are also O(N²)
   *resident*. This is Effect A.

### Fix options for Finding 2

**Measured breakdown of Effect A (cmd/fern-built `asm_run` driver, peak
RSS, fresh process each).** Two experiments split the cost:

| variant | 1×800 | 1×1600 |
| --- | --- | --- |
| baseline | 536 MB | 1576 MB |
| `OpsBuilder` ops cons-list (ops clone removed) | 406 MB | 1077 MB |
| 1 local, 800 op-heavy stmts (no `local_*` growth) | 158 MB | — |

So the super-linear cost splits **~30 % ops-array cloning / ~70 %
`local_*` cloning**, and both halves are independently super-linear. Two
consequences:

1. **`OpsBuilder` alone is not enough.** Routing ops through the
   clone-free cons-list (applied to `irlower.fern`, built by the *native*
   `cmd/fern` which compiles it correctly) drops 1×800 only 536→406 MB
   (~24 %) and stays super-linear. It is also **fixpoint-blocked** (#3554:
   the self-host AST backend miscompiles the recursive union when the
   >512-fn compiler self-compiles via the AST fallback). Worth landing for
   the ~30 %, but only after the AST-backend bug is fixed (goal #1).
2. **The dominant ~70 % is `local_*` threading** — every `with_local_*`
   does `s.local_<f>.with(i, v)` inside the 45-field rebuild, where the
   array is shared (rc≥2) so it clones instead of taking the in-place
   rc==1 path. (A standalone analog — `s = s.emit(v)` with `s.a.append(v)`
   even through a 31-field struct — stays at 9 MB because the append *is*
   in-place there; the self-host keeps `s.local_*` at rc≥2, so it clones.)

Fix options for the dominant half:

- **Mutable side-table ownership.** Hold the 17 `local_*` arrays so
  updates hit the in-place rc==1 path (single-owner reassign), instead
  of cloning out of a shared immutable record. Removes the quadratic
  and most of the box churn. Largest correctness surface (LowerState
  is passed by value through hundreds of functions).
- **Collapse the 17 parallel `local_*` arrays into one `LocalInfo[]`**
  (struct-of-arrays → array-of-structs). One `.with()` per local
  update instead of 17 record rebuilds; cuts the constant by ~17× and
  shrinks each rebuilt box. Does not by itself remove the clone-vs-
  share quadratic (the single `LocalInfo[]` still clones per local-add
  out of the shared record), but is a contained, independently-valuable
  step that stacks with the in-place rework.
  > **LANDED.** Done: `LowerState`'s 20 `local_*` arrays are now one
  > `locals: LocalInfo[]` (with `li_new` + per-field `li_set_*` helpers).
  > The per-statement `LowerState` rebuild dropped from 45 → 26 fields,
  > so it copies one local-table pointer instead of 20. **Measured
  > ~30 % peak-RSS reduction** on a local-heavy single function (getrusage,
  > `cmd/fern`-built `asm_ir_run`): 200 locals 83 → 60 MB, 400 locals
  > 205 → 143 MB, 600 locals 390 → 269 MB. The shape stays super-linear
  > (the clone-vs-share quadratic is untouched, as predicted) — this is
  > the constant-factor step; lifting the 512 cap still needs the in-place
  > rework below. Validated by the self-host fixpoint + the x86-64 / wasm
  > IR differential matrices; the `local_*` field docs moved to `LocalInfo`.
- The clone-free cons approach that works for ops (`OpsBuilder`) would
  also work for the local table, but inherits the same AST-backend
  union-miscompile blocker — so the mutable/in-place route is the
  unblocked path for the dominant half.
- Either way, **Finding 1 must land too** — even an O(N) allocation
  count OOMs at this scale if nothing is freed.

## Recommendation

Both fixes are core-infrastructure changes that must be proven on the
full CI matrix; neither is the "port a freelist" task originally
imagined (the freelist already exists — it is starved). Recommended
order:

1. **Finding 1 (the #3425 / Phase-1e no-free dec).** It is the
   dominant term (Effect B), it blocks lifting the cap, and it
   benefits *every* program, not just the self-compile. Execute under
   `docs/RC-PERCEUS-PHASE-1E-PLAN.md`.
2. **Finding 2**, starting with the contained `local_*` collapse, then
   the mutable-ownership rework if the quadratic still bites on the
   largest real functions.
3. Only then attempt to raise / remove the 512-function cap, gated by
   re-running the measurement above on the real merged bundle.

## Attempt log — Finding 2 via a cons-list `OpsBuilder` (BLOCKED)

The cleanest O(M) fix for Effect A is to stop threading `ops` as a
clone-on-append `ir.Op[]` and instead use a reverse cons-list builder
that shares structurally:

```
pub struct OpsNil  { pad: i32 }
pub struct OpsCons { op: ir.Op, rest: OpsBuilder }
pub type   OpsBuilder = OpsNil | OpsCons;
// ops_push = O(1) ref-prepend (never clones); ops_flatten = O(M), once
// per function; LowerState.ops: ir.Op[]  ->  OpsBuilder.
```

This was implemented and **works under the native Go backend** — all
100 `*IRX86_64` self-host IR tests pass, `-check` is clean on every
backend entry. But the **self-host fixpoint regresses**, and the
bisection is conclusive and worth recording:

- `TestSelfHostModloadFixpointX86_64` keeps the binaries byte-identical
  (`mmc == gen2 == gen3`) but **gen2-compiled programs drop their whole
  body**: `add(19,23)` exits 0, not 42.
- Paradox resolved: the ~1000-function compiler self-compiles via the
  **legacy AST fallback** (it is over the 512-fn cap), so `OpsBuilder`
  is never exercised *while building the compiler* — hence the
  byte-identical reproduction. The **small** test programs are under the
  cap, take the IR path, and run `gen2`'s self-host-compiled
  `OpsBuilder`, which is broken.
- Fast repro (≈1 s/probe, no 70 s rebuild): build the modload driver
  native (`mdriver`), have it emit the compiler's own asm, link that as
  `mmcBin`, then compile a one-line `add` with each. `mdriver` → 42,
  `mmcBin` → 0. `mmcBin` emits each function's prologue + param copies
  then `movq $0,%rax; ret` — **every body is dropped**.
- Reading the AST backend's emission of the helpers in the compiler's
  own asm (`__fn_irlower__ops_{empty,push,flatten}`) shows they are
  **emitted correctly** — right shape tags (`OpsCons` = `.S425`),
  field offsets (op@8, rest@16), match arm, reverse loop, and return.
  So the fault is **not** in the helpers.

Conclusion: the breakage is *uniform* (every IR-path body dropped) and
the helpers are correct, which localises it to the **self-host AST
backend mis-emitting the `LowerState` threading once `ops` is a
union-typed field** in that 45-field, pervasively-by-value-threaded
record. (A union-typed *field* works in the small — `ExprBinary.left:
Expr` is one — so the trigger is the union field inside the large
threaded state struct, not unions per se.)

### What this means for the fix order

The O(M) cons-list fix is **correct** (native proves it) but **blocked
by a legacy-AST-backend bug**, and that backend is the one being
retired. Three ways forward, cheapest-lever first:

1. **Unblock via goal #1, not a backend patch.** Once the IR subset
   covers the whole compiler, the >512-fn AST fallback retires; the
   compiler then self-compiles through the IR path, under which
   `OpsBuilder` already works. This is the standing roadmap goal anyway
   — so Finding 2's O(M) rewrite should land *after* the AST fallback
   is gone, not before.
2. **Fix the AST backend's union-typed-field threading** in `asm.fern`
   directly. Self-contained repro above (`mmcBin` compiling `add`), but
   it is deep asm-level work in a backend slated for removal — low
   leverage.
3. **Sidestep the union field.** Any O(M) structure-sharing rep needs a
   recursive type; a recursive *struct* with a sentinel hits the same
   backend surface. No clone-free representation was found that avoids a
   recursive/union field, so this is not currently viable.

Net: keep the recommended order above, but treat the Finding-2 O(M)
rewrite as **gated on retiring the AST fallback** (goal #1). The
`local_*` collapse (Effect-A constant factor) and Finding 1 (Effect B)
remain unblocked and independently landable.

## Finding 2 RE-MEASURED (2026-06-21) — the rebuild is NOT the problem

The original Finding 2 attributed the super-linear cost to the
`LowerState` rebuild cloning its arrays (`.append` / `.with` taking the
clone path because the record is rc≥2). A fresh round of direct
measurement — `cmd/fern`-compiled microbenchmarks that isolate one
variable at a time, plus the real `asm_run` IR driver — shows that
framing is wrong, and pinpoints the actual mechanism.

### The native in-place reuse works (the rebuild is in-place)

Microbenchmarks (45-field struct mirroring `LowerState`, peak RSS via
`wait4`/`ru_maxrss`, 8000 iterations):

| shape | peak RSS | verdict |
| --- | --- | --- |
| `s = s.emit(x)` clean self-reassign, 45 fields | **2.1 MB** | in-place |
| field count swept 20 → 60 | flat 2.1 MB | **no field-count threshold** |
| struct with **20 arrays**, `add` appends to ALL in one rebuild | **2.1 MB** | multi-array rebuild in-place too |
| thread `s` through a helper (`s = step(s,i)`) | **2.7 MB** | function boundary fine |
| **read `s.field` AFTER `step(s,i)`** (s kept alive) | **278 MB** | ← the clone |
| same, but hoist the read BEFORE the call (s moved) | **2.7 MB** | fix confirmed |

So: the 45-field rebuild, multi-array append, and helper threading are
**all in-place**. The clone fires for exactly one reason — the threaded
state is **read after it was passed into a call**, which keeps it live
(rc≥2) so the in-place reuse is disabled. The fix for any such site is a
one-line **hoist** (move the read before the threading call, or read the
equivalent field from the threaded result), not a structural change.

### The real cost is mostly LINEAR (Effect B), not the quadratic

Real `asm_run` (cap temporarily raised), peak RSS:

| workload | measurement | scaling |
| --- | --- | --- |
| 800 stmts, vary ops/stmt (~4 → ~14) | 122 → 440 MB | **linear in ops** (3.6× for 3.5×) |
| vary local count 200 → 3200 | 117 → 2502 MB | marginal 0.4 → 1.0 MB/local |

Tripling ops-per-statement triples memory — *linear*, not the O(ops²) a
clone would give. The per-local marginal rises then settles (~0.8
MB/local), i.e. a dominant **linear leak** (~0.4–1 MB per local /
statement) with a mild super-linear tail. A targeted hoist applied to a
real read-after-thread site in `lower_expr`'s `ExprBinary` arm changed
peak RSS by **zero**, confirming the rebuild-clone is not the resident
cost here; the well-threaded `lower_block` / `lower_stmt` / `StmtVar`
paths are already clean (read `s` *before* the thread point, operate on
the result *after*).

### Revised conclusion

- **Plan A (move `ops`/`local_*` out of `LowerState`) is not the fix.**
  It targets the rebuild clone, which is already in-place under the clean
  threading the code already uses. `docs/EFFECT-A-LOWERSTATE-INPLACE-PLAN.md`
  is superseded by this section.
- The dominant term is a **linear per-allocation leak** (Effect B —
  candidates: the asm-emit string building in `EmitState`, and leaked
  `Op.kind`/`Op.str` strings that ride the persistent op stream). The
  mild super-linear tail is a **specific keep-alive** that stops
  superseded `LowerState`s from being reclaimed — once located (needs
  rc/heap instrumentation in the self-host runtime, since the source
  reads cleanly), it is a cheap localized hoist, not a refactor.
- Next memory work, if resumed, should **locate the keep-alive + profile
  the linear leak** first, rather than execute a structural rework.

## Reproduction

```sh
go build -o /tmp/fern ./cmd/fern
# temporarily: asm_ir.fern  `> 512`  ->  `> 100000`
/tmp/fern -target x86-64-linux -o /tmp/asm_run examples/self_host/asm_run.fern
# feed generated N-function / M-statement modules on stdin; measure
# peak RSS with getrusage(RUSAGE_CHILDREN).ru_maxrss

# Self-host-backend repro for the OpsBuilder blocker (≈1 s/probe):
#   mdriver = cmd/fern build of asm_modload_run.fern (native backend)
#   mmcBin  = link of (mdriver compiling the compiler's own source)
#   echo 'function add(x:i32,y:i32):i32{return x+y;} \
#         function main():i32{return add(19,23);}' > add.fern
#   mdriver add.fern -> 42 ;  mmcBin add.fern -> 0 (body dropped)
```
