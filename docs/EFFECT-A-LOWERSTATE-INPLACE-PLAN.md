# Effect A — `LowerState` in-place accumulation plan

Date: 2026-06-21.
Status: design / blueprint. The measurement that motivates it is in
`docs/IR-SELFCOMPILE-OOM-FINDINGS.md` (Finding 2 + the quantified
breakdown).

## The target

The self-compile's super-linear memory (Effect A) is, by measurement:

- **~30 % ops-array cloning** — `(s) emit(op)` rebuilds `LowerState`
  with `ops: s.ops.append(op)`; because the 45-field rebuild keeps `s`
  alive, `s.ops` is rc≥2 and `.append` **clones** instead of taking the
  in-place rc==1 path. O(M²) per M-op function.
- **~70 % `local_*` cloning** — each `with_local_*` does
  `s.local_<f>.with(i, v)` inside the same rebuild; same rc≥2 → clone.
  17 parallel arrays, O(N²) per N-local function.

Both have the **same root**: an array that lives inside a by-value
record is rc≥2 whenever the record is rebuilt, so every update clones.
The fix is to update those arrays as **sole-owner self-reassigned
locals** (`a = a.append(v)` / `a = a.with(i, v)`), which the native
backend lowers in place (amortised O(1)). Verified: every standalone
analog (`s = s.emit(v)` with `s.a.append(v)`, even through a 31-field
struct) stays at 9 MB because the append *is* in-place there.

## Why this is one atomic refactor (no incremental slice)

`ops` (and the `local_*` table) are threaded **through** the lowering:
`lower_func` → `lower_block` → `lower_stmt` → hundreds of expression
helpers, each `(…, s: LowerState) → LowerState`, accumulating via 667
`.emit(...)` sites. To move `ops` to a sole-owner local it must leave
`LowerState` and be **returned alongside** it — every signature and every
call site changes together, or `irlower.fern` does not compile (Fern
fields are immutable — E048 — so there is no "mutate in place and keep
the old signature" shortcut).

Scope (current `irlower.fern`):

| surface | count |
| --- | --- |
| `.emit(` call sites | 667 |
| functions returning `LowerState` | 54 |
| `s: LowerState` params | 238 |

## Plan A — full in-place threading (the real fix, ~100 % of Effect A)

1. **Return type.** Change the threaded lowering functions from
   `→ LowerState` to `→ (LowerState, ir.Op[])` (Fern has tuples). `ops`
   leaves `LowerState` entirely. The 17 `local_*` arrays get the same
   treatment in a second pass (they can ride in a small `Locals` value
   that is *also* threaded as a returned tuple element, never rebuilt).
2. **emit.** `s = s.emit(op)` → `ops = ops.append(op)` (sole-owner
   self-reassign → in-place amortised append). `emit`'s ctrl-depth
   bookkeeping moves to a tiny `(s, op) → s` helper or inlines.
3. **Threading.** Every `var s = f(…, s)` becomes
   `var (s, ops) = f(…, s, ops)`; the callee moves `ops` in (last use)
   so it stays rc==1 and appends in place.
4. **Finalisation.** `lower_func` seeds `var ops: ir.Op[] = []`, threads
   it, and puts it straight into `LowerResult.ops` (no flatten needed —
   it is already a forward-order `ir.Op[]`).
5. **Validate** (every step is meaningless until the whole thing
   compiles): `go test ./internal/ir`, the x86-64 `rc_correctness`
   corpus + freelist, the **self-host fixpoint**
   (`TestSelfHostModloadFixpointX86_64`), and the self-host IR e2e
   matrix. This is non-union, so it does **not** hit the #3554
   AST-backend blocker that sank the `OpsBuilder` cons-list.

Effort: large but mechanical; the risk is missing a thread site (the
compiler catches it — it won't build) or a move that leaves `ops` rc≥2
(the fixpoint catches it — wrong output). Do it in one focused pass.

## Plan B — chunked `ir.Op[][]` (ATTEMPTED — also AST-backend-blocked)

The idea was to recover the ops half **without** touching the 667 sites,
keeping `ops` inside `LowerState` but as a chunked `ir.Op[][]` (a list of
≤128-op chunks) so `emit` clones only the last chunk + the short outer
list instead of the whole flat array. It is **not** a union, so it was
expected to dodge #3554.

**Implemented and measured — it works under `cmd/fern` but is
fixpoint-blocked, exactly like `OpsBuilder`:**

- `cmd/fern`-built driver: correct output, and the win lands — 1×800
  **536→419 MB**, 1×1600 **1576→1104 MB** (~the OpsBuilder number).
- `TestSelfHostModloadFixpointX86_64` **FAILS**: `gen2` (the self-host
  **AST**-compiled compiler) miscompiles `add(19,23)`→0 and segfaults —
  the identical failure mode as the `OpsBuilder` union (#3554).

**Conclusion (the new finding):** the self-host AST backend's blocker is
**not specific to unions** — it mis-emits the threading whenever
`LowerState.ops` is *any* compound-typed field (recursive union *or*
nested `ir.Op[][]`). A plain `ir.Op[]` field compiles fine (that's the
status quo). So **no in-`LowerState` representation change for `ops` can
be merged** while the >512-fn compiler self-compiles via the AST
fallback. Both localized options (OpsBuilder, chunked Op[][]) are dead
ends.

Only two paths remain unblocked:

1. **Plan A** — move `ops` *out* of `LowerState` to a separately-threaded
   plain `ir.Op[]` (the AST backend handles plain array params/returns
   fine). 667 sites, atomic.
2. **Retire the >512-fn AST fallback** (roadmap goal #1) so the compiler
   self-compiles through the IR path, which lowers unions / nested arrays
   correctly — at which point the localized OpsBuilder/Op[][] fixes become
   mergeable. This also fixes the dominant `local_*` 70 % for free if that
   is migrated the same way.

Either way the dominant `local_*` 70 % still needs Plan A's
separate-threading treatment (or goal #1). There is no safe localized
down-payment; the ops and local halves are gated on the same structural
change.

## Reference

- `docs/IR-SELFCOMPILE-OOM-FINDINGS.md` — measurement + breakdown.
- `examples/self_host/irlower.fern:255` — `(s) emit(op)`.
- `examples/self_host/irlower.fern:12854` — `lower_func` (seeds state).
- #3554 — why the union (`OpsBuilder`) route is AST-backend-blocked;
  both plans here are non-union and avoid it.
