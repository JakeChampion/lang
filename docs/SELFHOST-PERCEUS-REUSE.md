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

---

## 6. Implementation notes (verified from source)

Decoded from `internal/ir/ir.go` + `internal/codegen/x86_64/x86_64.go` +
`examples/self_host/asm_ir.fern`, resolving the §5 open questions so Slice 0/1
codegen is turn-key:

### 6.1 Native `__fern_alloc_reuse(token, tokenSize, size)` (`emitAllocReuseRuntime`)

Pure, no rc check of its own — the **caller** guarantees `token` is unique-or-null:

1. `token == 0` → tail-call `__fern_alloc(size)` (fresh).
2. else compare `class(tokenSize)` vs `class(size)`; **class match** → return
   `token` (reuse the block in place).
3. **mismatch** → `__fern_free(token)` (raw free by base), then fresh
   `__fern_alloc(size)`.

### 6.2 The call-site sequence (`emitReuseToken`) — the op recipe to mirror

For a construction C paired with dead owned local D (base = `D_data − 8`):

```
uniq  = __fern_rc_is_unique(D)          # i32, into a fresh local
if uniq { token = D_data − 8 }          # base pointer
else    { __fern_rc_dec(D); token = 0 } # shared → D's alias keeps the box
D_slot = 0                              # D consumed: zero its slot
base   = __alloc_reuse(token, D_alloc, C_alloc)   # box base, where OpAlloc's would sit
```

Then `emitReuseOldFieldDrops` deep-drops D's OLD pointer fields **gated on
`uniq`** (fresh box on the decline branch needs no drop), before C's stores
overwrite them. The self-host emits the identical `ir.Op` sequence
(`op_load_local` / `op_call_direct("__fern_rc_is_unique", 1)` / `op_if` /
`op_store_local` / …) — `irlower` already uses exactly these ops.

### 6.3 Self-host allocator specifics (differ from native — must adapt)

- **Freelist class is 8-byte-granular**: `__fern_alloc` rounds `(bytes+7)&−8`,
  then `class = bytes/8`, `< 65536`; `__fern_freelist` is 65536 × 8-byte
  buckets, an intrusive singly-linked free list (next-pointer at the block's
  first word). Native's class is `(bytes+15)&−16`. **The self-host
  `__fern_alloc_reuse` must class by `bytes/8`** (two boxes reuse iff
  `size1/8 == size2/8`, i.e. equal when both 8-aligned).
- **No standalone `__fern_free`**: the freelist-push lives inside the rc-guarded
  `__fn___fern_arr_dec` (push at rc==1). Slice 0 must factor a raw
  `class`-indexed push (`freelist[size/8] = block; block[0] = old_head`) out of
  that path — the mismatch branch frees a **known-unique** block, so it can push
  directly without the rc guard.
- **Box base / header**: struct boxes are `[rc@base, field0@base+8, …]` (rc is
  the 32-bit word at `data−8`, matching `__fern_rc_is_unique`'s `movl -8(%rcx)`),
  so `token = data − 8` and `alloc size = 8 + nfields*8`. (Array boxes differ —
  `[cap, rc, len, e…]`, base = `data−16` — which is why the first slice targets
  **structs only**; tuples/enums come later with their own base math.)

### 6.4 The invasive point: `op_struct_make`

The self-host lowers `T{…}` via a single `op_struct_make(type_name, ndecl)`
(irlower ~L4351) that the backends expand to alloc + rc-init + field stores.
Reuse must divert the ALLOC half only. Two options:

- **(a) inline at the reuse site** in `irlower`: when the self-overwrite /
  pairing fires, emit the §6.2 recipe + explicit `op_struct_set` field stores
  instead of `op_struct_make`, so `op_struct_make` itself is untouched
  (backend-agnostic; keeps the risky change in one file). **Recommended for
  Slice 1.**
- **(b) a reuse-aware `op_struct_make_reuse`** carrying a token operand — cleaner
  long-term but touches every backend's struct-make handler. Defer.

### 6.5 Gate

Add `reuse_enabled` to `EmitState` (default **false**), mirroring native's
`RcReuseEnabled`. All of Slices 0–4 land behind it; a final isolated PR flips the
default once every slice's differential + both fixpoint suites are green — so any
regression bisects to that one-line flip.
