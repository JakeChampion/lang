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
statement**. That is far more than a single Op box; peak tracks the
**sum of every op allocation ever made — across all functions and all
lowering passes — not the live set.** That is the signature of a
non-freeing allocator (Finding 1), independent of the quadratic.

A flat AST changes neither table: both scale with *IR allocations
performed during lowering*, not with AST-node storage.

## Finding 1 — RC `dec` never frees (the #3425 leak)

`internal/codegen/x86_64/x86_64.go:emitRcDecRuntime` (`__fern_rc_dec`)
is explicit:

> Phase-1 simplification: on rc == 1 the helper still decrements to 0
> instead of calling a type-specific drop handler + freelist push.
> **The bump allocator leaks.**

A recycling two-tier freelist (`__fern_free`, `emitAllocRuntime` /
`emitFreeRuntime`) *does* exist and works — but it is **starved**.
Only the array drop handler (`__fern_drop_arr_ptr`) returns its buffer
to it; the generic dec used for **struct / enum / string boxes** just
writes rc=0 and walks away.

`ir.Op` (`examples/self_host/ir.fern:55`) is a struct with string
fields, so an `Op[]` is an array of **heap-boxed `Op` structs**. The
array *buffer* recycles via `__fern_drop_arr_ptr`, but every
individual `Op` box and its `kind`/`str` strings drop through the
non-freeing generic dec. Across ~1000 functions × hundreds of ops ×
multiple lowering passes, those boxes accumulate until the heap is
exhausted. This is Effect B.

This is the native half of the already-planned work in
`docs/RC-PERCEUS-PHASE-1E-PLAN.md` (widening RC from arrays to
structs/strings/enums/closures). That plan also documents *why* it is
delicate: opaque runtime structs (Reader, Writer, Map, HttpRequest, …)
have no rc word, so a blind "free everything on dec" corrupts the
heap. The fix must emit **type-specific drop handlers** (or carry an
allocation size in the box header) so only genuinely rc-tracked,
user-allocated boxes are returned to the freelist.

Scope: native runtime change, mirrored across x86-64 / arm64 / wasm.
High blast radius (heap corruption class of bug). Must be validated on
the full e2e + arm64/qemu + wasm matrix, not x86-64 alone.

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
