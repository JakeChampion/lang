# Design: porting Perceus constructor-reuse (FBIP) to the self-hosted compiler

Status: **proposal / design** (no codegen yet). This is the design step for
#2649 goal 2 — matching the self-hosted compiler's memory management to the
native compiler's. The RC/ownership slices (inc/dec insertion, borrow inference,
per-type deep `__struct_drop_<T>` / `__field_reclaim_<T>`, snapshot-param
reclaim) have already landed on the self-host IR path. The one large remaining
piece is **constructor reuse** — the Perceus "reuse token" / FBIP (functional
but in-place) optimisation. This document describes the native mechanism, the
self-host's current state, a sliced port plan, the soundness invariants, and the
testing strategy, so the implementation can proceed incrementally and safely.

Why it matters: reuse turns a `free(old) + alloc(new)` pair at a construction
site into an in-place overwrite of the old box — zero-alloc updates for the
common "consume a value, build a same-shaped value" pattern (map/fold/tree
traversals, per-iteration struct churn). It is the difference between the
self-hosted compiler allocating on every loop iteration and reusing one box.

**This is memory-safety-critical.** A mis-paired or under-guarded reuse in the
self-hosted compiler is a use-after-free / double-free that miscompiles *every*
program the self-host builds. Every slice must be refcount-guarded, gated, and
validated by byte-for-byte differential + fixpoint tests before it is enabled.

---

## 1. What the native compiler does

Native reuse lives in `internal/ir/ir.go` (the builder), gated on two
independent flags in `internal/ast/ast.go`:

- `RcFreeEnabled` (default true) — the Phase-3 freelist allocator (recycling
  freed boxes). Reuse only makes sense when freeing.
- `RcReuseEnabled` (default true) — the constructor-reuse (FBIP) layer, a
  *separate axis on top of* `RcFreeEnabled`.

The moving parts:

| Native function (`ir.go`) | Role |
|---|---|
| `computeReuseSources()` | The general-FBIP **pairing analysis**: matches a construction site C (`var c = T{…}` / a tuple lit) to a **dead, owned** struct/tuple local D of the exact same box layout whose box C can repurpose. Returns the C→D map + the set of consumed D names (so precise-drop won't *also* drop them). |
| `structReuseEligible` / `tupleReuseEligible` / `enumReuseLoads` | **Eligibility**: box sizes match exactly; fields are i32-class scalars or single-word rc-tracked pointers (strings + wide/float scalars excluded); enum layout uniform; box ≤ 2048 (exact-fit freelist class). |
| `reuseClassOf` / `reuseSourceLayout` | The box "kind" (struct/tuple/enum), type name, and freelist class `(alloc+15)&-16` of `data + 8-byte rc header`. |
| `emitReuseToken(dName, dAlloc, cAlloc)` | **Codegen for the reuse allocation**: at C, if D's box is uniquely owned, reuse it; else fall back to a fresh alloc. |
| `emitReuseOldFieldDrops(...)` | On the reuse branch, **deep-drops D's old pointer fields** before C's stores overwrite them (D is dead, so it carries nothing into C). |
| `tryStructReuseOverwrite` / `tryEnumReuseOverwrite` | The **self-overwrite** special case (Phase 5b/5c): D == C, i.e. `a = T{…}` reassigning an owned local `a` reuses `a`'s own old box. Simpler than the general cross-local case. |
| `consumingReuseCtor` | The **consuming-match FBIP** traversal: `match (x) { V(p) => W(f(p)) }` reuses x's variant box for W. |

Runtime support (emitted on demand by the backends, e.g.
`internal/codegen/x86_64/x86_64.go`):

- `__fern_alloc_reuse` (gated on `usesAllocReuse`) — given a candidate dead box
  pointer and a size, return that box if it is live + uniquely owned (rc == 1)
  and the right freelist class, else allocate fresh.
- `__fern_rc_is_unique` (gated on `usesRcIsUnique`) — 1 iff a live heap object
  has rc == 1. The second, *runtime* gate: a statically-paired-but-shared D
  degrades to dec-then-fresh-alloc — never unsound, never a leak.

### The invariant (why it's sound)

A reuse is the pair **(static pairing, runtime guard)**:

1. **Static** (`computeReuseSources`, deliberately narrow first cut):
   - D and C are the same *kind* (struct↔struct / tuple↔tuple / enum↔enum) and
     the same exact box layout / freelist class.
   - D is a `var`, declared before C in the **same block** (function body, loop
     body, or if-arm), never reassigned, name-unique (no shadowing).
   - D is `freeEligible` — **owned**, never a borrowed param.
   - D is **dead from C onward** within its block: referenced in no statement at
     or after C's index. So C's fields never read D, and nothing observes D's box
     after C repurposes it.
   - The reuse **zeroes D's slot**, so the block-exit sweep (and any path that
     does *not* reach C) null-guards to a no-op / drops D normally.
2. **Runtime** (`__fern_alloc_reuse` → `__fern_rc_is_unique`): only reuse if D's
   box is uniquely owned *at that moment*. A shared D (rc > 1) copies —
   dec-then-fresh-alloc. This is the safety net that makes a conservative static
   analysis sound even under aliasing the static pass can't see.

A mispaired or shared D therefore always degrades to the pre-reuse behaviour;
it is *observationally transparent* — the native differential gates run
free-off vs free-on **and** reuse-off vs reuse-on and require byte-identical
results (see `internal/e2e/testdata/cases/general_reuse_struct/main.fern`).

---

## 2. What the self-hosted compiler has today

Already ported to the self-host IR path (`irlower.fern` + `asm_ir.fern` /
`asm_arm64_ir.fern`):

- The **freelist allocator** + rc header (equivalent to `RcFreeEnabled`).
- **inc/dec**: `__fern_rc_inc` (Perceus dup), `__fern_rc_dec` /
  `__fern_arr_dec` (release), exit-sweep drops.
- **Deep drop**: per-type `__struct_drop_<T>` (deep-drops rc-array struct
  fields) and `__field_reclaim_<T>` (frees replaced array-field buffers), plus
  `__fern_snapshot_dec` (snapshot-param consume-rebind reclaim).
- **`__fern_rc_is_unique`** — **the runtime uniqueness guard already exists**
  (`asm_ir.fern`, `__fn___fern_rc_is_unique`; also in `is_fern_helper` /
  `ir_helper_symbol`). This is the key building block: the reuse port does *not*
  need to invent the runtime guard, only the `alloc_reuse` wrapper around it.
- Aliasing analysis for **`.with()`** in-place vs clone (`aliased_names`): an
  `a = a.with(i, v)` self-reassign lowers in place only when `a` is unaliased,
  else a value-producing clone (`lower_arr_with_value`) — *"correct, if leak-y,
  ahead of the full Perceus refcount-guarded reuse"* (`irlower.fern` ~L355).

**Not yet ported**: the constructor-reuse layer itself — `computeReuseSources`
(the C→D pairing), `emitReuseToken` (the reuse-vs-fresh alloc branch),
`emitReuseOldFieldDrops` (old-field deep-drop on the reuse branch), the
self-overwrite `tryStructReuseOverwrite`, and the `__fern_alloc_reuse` runtime
wrapper. That is the scope of goal 2's remaining work.

---

## 3. Sliced port plan (smallest-safe-first)

Each slice is a standalone PR: gated **off by default** behind a self-host
reuse flag until its differential + fixpoint tests are green, then flipped on in
a follow-up once proven. Ordering is by ascending analysis complexity.

- **Slice 0 — runtime + gate.** Add `__fern_alloc_reuse(candidatePtr, size)`
  (reuses the existing `__fern_rc_is_unique`) on the x86-64 IR and arm64 IR
  paths, plus a `reuse_enabled` gate in `EmitState`. No pairing yet; pure
  plumbing + a unit test that the helper returns the candidate iff rc==1 and the
  size/class matches, else a fresh box. **Lowest risk; unblocks the rest.**
- **Slice 1 — struct self-overwrite.** Port `tryStructReuseOverwrite`: `a =
  T{…}` where `a` is an owned, unaliased, `structReuseEligible` struct local —
  reuse `a`'s own dead box (deep-drop its old fields, overwrite in place). D==C,
  so no cross-local liveness needed; this is the simplest sound case.
- **Slice 2 — general FBIP struct (cross-local).** Port `computeReuseSources`
  for structs (the `general_reuse_struct` case: dead local `a` reused for a
  later same-type `b`). Requires the block-scoped dead-from-C liveness walk +
  slot-zeroing + consumed-name bookkeeping so precise-drop doesn't double-drop.
- **Slice 3 — tuples.** Extend the pairing to tuple↔tuple
  (`tupleReuseEligible`, the `general_reuse_tuple` case).
- **Slice 4 — enums + consuming match.** `enumReuseLoads` + `consumingReuseCtor`
  (the `enum_reuse_churn` / `c2_consuming_reuse` cases) — highest complexity
  (non-uniform variant layouts, the consuming-match traversal); last.

Each slice mirrors an existing native `internal/e2e/testdata/cases/*reuse*`
case, so the target behaviour is pinned to a known-good oracle.

---

## 4. Risks & testing

- **Memory corruption** is the dominant risk. Mitigations: (a) the runtime
  `is_unique` guard makes every reuse degrade-safe; (b) each slice ships a
  differential e2e that stresses alloc/drop/reuse churn (the native
  `*reuse*/main.fern` cases) and compares self-host output to the interpreter
  **byte-for-byte**; (c) **both fixpoint suites must still converge** — reuse
  changes the emitted asm, so `TestSelfHostLoadFixpointX86_64` /
  `TestSelfHostModloadFixpointX86_64` prove the self-compiled compiler is still
  self-consistent; (d) slices land **gated off** and flip on only after green.
- **Analysis divergence from native.** The self-host pairing must be *no more
  aggressive* than native's (conservative is safe — it only forgoes an
  optimisation). Reuse-on-self-host vs reuse-off-self-host must be
  observationally identical, exactly as native's reuse-on vs reuse-off gate
  asserts.
- **Fixpoint sensitivity.** Because the self-hosted compiler's own source will
  be compiled *with* reuse once enabled, a subtle bug surfaces as a fixpoint
  divergence (or a crash compiling the compiler). Keep the flag off through
  Slices 0–4; enable in a final, isolated "flip reuse on" PR whose sole change
  is the default, so a regression is trivially bisectable.

## 5. Open questions

- **Box-size / freelist-class parity.** Native's exact-fit `≤ 2048`,
  `(alloc+15)&-16` over `data + 8-byte rc header` must match the self-host
  freelist's class boundaries exactly, or `alloc_reuse` will hand back a
  wrong-class box. Confirm the self-host freelist uses the identical class
  function before Slice 0.
- **Where the pairing runs.** Native computes reuse sources per-function in the
  builder before lowering statements. The self-host equivalent is a pre-pass
  over each `FuncDecl` in `irlower` producing a C→D map threaded into
  `lower_stmt` — confirm the cleanest insertion point (alongside the existing
  precise-drop / aliased-names pre-passes).
- **Interaction with the existing `.with()` aliasing path.** Once general reuse
  lands, the "leak-y clone" fallback for aliased `.with()` can potentially be
  upgraded to refcount-guarded in-place reuse — a nice follow-up, out of scope
  for the initial port.
