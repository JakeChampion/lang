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

## Reproduction

```sh
go build -o /tmp/fern ./cmd/fern
# temporarily: asm_ir.fern  `> 512`  ->  `> 100000`
/tmp/fern -target x86-64 -o /tmp/asm_run examples/self_host/asm_run.fern
# feed generated N-function / M-statement modules on stdin; measure
# peak RSS with getrusage(RUSAGE_CHILDREN).ru_maxrss
```
