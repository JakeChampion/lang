# Self-host IR self-compile OOM — root-cause findings

Date: 2026-06-20.
Status: investigation complete; fixes scoped, not yet implemented.

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

**Remaining native string drop sites** (each its own incremental,
poison-validated slice): the function-exit sweep / reinit drop of a bare
string local, `emitOwnedTempStackDrop` (fresh concat/slice temps,
provably sole-owner so trivially safe), and string **elements** of
arrays / tuples / enums / closure captures. Each routes its
`__fern_rc_dec` to `__fern_str_dec` **only after** confirming the
matching alias site emits the balancing inc on native (some specialized
two-word `str_inc` sites — tuple element, `Map[K,string]` get/get_or —
have **no** native `rc_inc` branch yet; those need the inc added first or
they would over-release).

Scope: x86-64-only (arm64/wasm already reclaim strings via the two-word
`str_inc`/`str_dec` pair), so each slice is fully **locally** testable.
Heap-corruption class — gate every slice on the x86-64 `rc_correctness`
corpus + freelist harness + a `RcFreeDebug` poison run. Tracks
`docs/RC-PERCEUS-PHASE-1E-PLAN.md`'s **Phase 1e-strings**.

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

- **Mutable side-table ownership.** Hold the 17 `local_*` arrays so
  updates hit the in-place rc==1 path (single-owner reassign), instead
  of cloning out of a shared immutable record. Removes the quadratic
  and most of the box churn. Largest correctness surface (LowerState
  is passed by value through hundreds of functions).
- **Collapse the 17 parallel `local_*` arrays into one `LocalInfo[]`**
  (struct-of-arrays → array-of-structs). One `.with()` per local
  update instead of 17 record rebuilds; cuts the constant by ~17× and
  shrinks each rebuilt box. Does not by itself remove the clone-vs-
  share quadratic, but is a contained, independently-valuable step.
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

## Reproduction

```sh
go build -o /tmp/fern ./cmd/fern
# temporarily: asm_ir.fern  `> 512`  ->  `> 100000`
/tmp/fern -target x86-64 -o /tmp/asm_run examples/self_host/asm_run.fern
# feed generated N-function / M-statement modules on stdin; measure
# peak RSS with getrusage(RUSAGE_CHILDREN).ru_maxrss

# Self-host-backend repro for the OpsBuilder blocker (≈1 s/probe):
#   mdriver = cmd/fern build of asm_modload_run.fern (native backend)
#   mmcBin  = link of (mdriver compiling the compiler's own source)
#   echo 'function add(x:i32,y:i32):i32{return x+y;} \
#         function main():i32{return add(19,23);}' > add.fern
#   mdriver add.fern -> 42 ;  mmcBin add.fern -> 0 (body dropped)
```
