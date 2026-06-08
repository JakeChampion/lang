# Porting Perceus RC to the self-hosted compiler

Status: **design + implementation tracker** (started 2026-06-07).
Goal: bring the native compiler's Perceus reference-counting +
compile-time memory optimisation to the self-hosted Fern compiler
(`examples/self_host/`), at feature parity. This document is the
roadmap; it mirrors `docs/RC-PERCEUS-PLAN.md` (the native rollout,
Phases 0–6 + Perceus, all shipped) and `docs/OWNERSHIP-INFERENCE-PLAN.md`,
mapping each native piece onto the self-host architecture.

A **direct port** is the explicit strategy: reproduce the native
algorithm step-for-step so we inherit its correctness story and can
diff behaviour against the native compiler. The native side is the
source of truth.

---

## 1. Where the two compilers stand today

### Native (Go) — fully implemented

Perceus in the native compiler lives almost entirely in the
target-agnostic IR-lowering builder, `internal/ir/ir.go` (~16k LOC),
plus reuse/TRMC passes (`internal/ir/trmc.go`,
`internal/ir/insert_resource_drops.go`). It has **no dedicated RC
opcodes** — RC is realised as `OpCallDirect` to named runtime helpers
(`__fern_rc_inc`, `__fern_rc_dec`, `__fern_rc_is_unique`,
`__fern_box_free`, `__fern_alloc_reuse`, generated `__drop_*` handlers)
emitted during lowering, guided by a set of pure static analyses. The
runtime helpers are emitted per backend
(`internal/codegen/arm64/arm64.go`, `internal/codegen/x86_64/x86_64.go`,
`internal/codegen/wasmbin/runtime.go`).

Native heap layout (the shape we must reach): rc at `[data-8]`, with
type-specific words below it (array `cap` at `[data-12]`, `len` at
`[data-4]`). Static/immortal cells carry the sentinel `0x80000000` in
the rc word; inc/dec short-circuit on it.

### Self-host (Fern) — no RC at all

The production self-host path is **AST → asm, directly**, with no IR:

```
source → lexer.tokenize → parser.parse_module
       → flatten.bundle → parser.module_with_builtins   (AST passes)
       → asm.emit_module / asm_arm64.emit_module / wasm.emit_module
```

- Shared frontend: `examples/self_host/asmcore.fern` — the `Ty` type
  system, `infer_expr_type`, `EmitState` + its state methods, the
  free-variable walker, and the pre-codegen `check_module`. CLAUDE.md:
  edit it **once**; it is not mirrored in the backends.
- Per-backend emit (instruction selection + runtime-helper bodies):
  `asm.fern` (x86-64), `asm_arm64.fern` (arm64), `wasm.fern` (wasm).
- The AST-pass hub is `parser.module_with_builtins`
  (`parser.fern:4467`): hoist local funcs → insert resource drops →
  lower defers → monomorphise funcs → monomorphise structs → inject
  builtin enums. **A Perceus AST analysis pass slots in here**, after
  monomorphisation (so it sees concrete types).

The self-host has **no rc slot in any heap layout** and the RC
intrinsics are no-op stubs (a leak-everything bump heap):

| Kind | Self-host layout today | Returned ptr | rc? |
|---|---|---|---|
| Array | `[cap, len, e0, e1, …]` | base+8 (at `len`) | none (`cap` at `[data-8]`) |
| String | `[data_ptr, len]` (16 B) | base | none |
| Tuple | `[v0, v1, …]` | base | none |
| Struct | `[shape_ptr, f0, f1, …]` | base | none |
| Enum | `[shape_ptr/tag, payload]` | base | none |
| Closure | `[fn_addr, cap0, cap1, …]` | base | none |

Stub bodies: `asm.fern:4947-4955`, `asm_arm64.fern:4673-4681`
(`__fern_rc_inc` returns its arg, `__fern_arr_dec` /
`__fern_drop_arr_ptr` / `__free` are bare `ret`). The wasm backend has
no RC machinery yet.

The only RC-adjacent code that exists is the **static `own`/move
checker** in `checker.fern:3387-3640` (E050/E051) — diagnostics, not
codegen. (Note: the asm path uses `asmcore.check_module`, a lighter
checker, not `checker.fern`.) `ParamDecl.own` is already parsed
(`parser.fern:174`).

### Consequence

A faithful port is essentially **re-running native Phases 0–6 +
Perceus on an rc-less base layout**, in the self-host's value-semantic
emit style, across two hand-maintained backends — gated by the
byte-identical self-bootstrap fixed-point. It is large and must be
landed in green, bounded slices.

---

## 2. Architecture mapping (native → self-host)

| Native (Go) | Self-host (Fern) | Notes |
|---|---|---|
| `builder` struct fields (mutable) | `EmitState` (immutable, threaded) | analyses become fields on a per-function analysis record |
| `ir.LowerWith` pipeline | `parser.module_with_builtins` + `emit_module` | analyses run as an AST pass / per-function precompute |
| `computeFreeEligible` (`ir.go:2574`) | `asmcore.fern` (shared) | pure AST analysis |
| `inferParamEscapes` (`ir.go:1336`) | `asmcore.fern` (shared) | call-graph fixpoint |
| `computeMovedLocals` / `markConstructionMoves` (`ir.go:3759/3903`) | `asmcore.fern` (shared) | move-on-return/alias/construction/destructure |
| `computePreciseDrops` (`ir.go:3294`) + helpers | `asmcore.fern` (shared) | last-use drop placement |
| `computeReuseSources` (`ir.go:12119`) | `asmcore.fern` (shared) | FBIP reuse pairing |
| `computeConsumedParams` (`ir.go:14536`) | `asmcore.fern` (shared) | owned-param promotion |
| `findTrmcFuncs` (`trmc.go`) | `asmcore.fern` (shared) | TRMC classification |
| `needsRcIncOnAlias` (`ir.go:14788`) | `asmcore.fern` predicate | the shared inc trigger |
| `emitAliasInc` (`ir.go:14771`) | per-backend `emit_*` | emits the helper call |
| exit-dec sweep `emitRcDecLocalsAtExit*` (`ir.go:3264/4004`) | per-backend `emit_function` | dec every rc-tracked local/param |
| typed drop generators `genStructDropFn`/`genEnumDropFn`/… (`ir.go:5767+`) | per-backend `emit_runtime` | generated `__drop_*` bodies |
| reuse-token emit `emitReuseToken` (`ir.go:12510`) | per-backend `emit_*` | `__alloc_reuse` call |
| runtime helpers `emitRcIncRuntime` etc. (`arm64.go`/`x86_64.go`/`runtime.go`) | per-backend `emit_runtime` | the `__fern_rc_*` bodies |

**Rule of thumb:** every *decision* (where to inc/dec/reuse/drop) is
target-independent → `asmcore.fern`. Every *emission* (the actual asm
sequence) → each backend's `emit_*`, duplicated like the existing
`__fern_alloc` call sites.

Because `EmitState` is immutable and threaded, the analyses are best
computed **once per function** and carried as parallel arrays keyed by
local-slot index / AST node identity, rather than mutated in place. The
self-host already does this style (e.g. `local_names`/`local_types`).

---

## 3. Heap-layout migration (the self-host "Phase 0")

The native rc lives at `[data-8]`. The self-host need not match native
byte-for-byte (it emits its own asm), only be **internally
consistent** — every alloc site and every field/element offset in
`asm.fern` + `asm_arm64.fern` + `wasm.fern` must agree. Target layouts
(chosen to disturb existing offsets minimally and to keep `len` where
readers already expect it):

| Kind | New layout (base →) | Returned ptr | rc at | other |
|---|---|---|---|---|
| Array | `[rc, cap, len, e0, …]` | base+16 (`len` − keep `arr[i]`=`(i+1)*8`? see note) | `[data-16]` | cap `[data-8]`, len `[data]` |
| String | `[rc, _, data_ptr, len]` | base+16 | `[data-16]` | data `[data]`, len `[data+8]` |
| Tuple | `[rc, _, v0, …]` | base+16 | `[data-16]` | v_i at `[data + i*8]` |
| Struct | `[rc, _, shape_ptr, f0, …]` | base+16 | `[data-16]` | shape `[data]`, field i at `[data+(i+1)*8]` |
| Enum | `[rc, _, shape/tag, payload]` | base+16 | `[data-16]` | unchanged field offsets |
| Closure | `[rc, _, fn_addr, cap0, …]` | base+16 | `[data-16]` | fn at `[data]` |

Note: the **cleaner** alternative is to mirror native exactly (rc at
`[data-8]`), shifting the returned pointer past a single rc word and
keeping cap below it for arrays. The decision (16-byte vs 8-byte
header, where `_`/pad goes) is finalised in slice 0a; what matters is
that the migration is **atomic per category** and the self-bootstrap
stays byte-identical (the emitter compiles its own source, so the
change is self-consistent as long as it is deterministic).

Migration tactic: introduce **named offset helpers** in `asmcore.fern`
(e.g. `arr_data_off()`, `arr_rc_off()`, `arr_len_off()`,
`struct_field_off(i)`, …) and route every backend offset literal
through them, so the layout is defined once and the two backends can't
drift. This is itself a valuable refactor independent of RC.

Sentinel: string literals in `.rodata`, the empty-array/empty-string
sentinels, and shape pointers are immortal → write `0x80000000` into
their rc word (or keep them out of the heap and have inc/dec
short-circuit on the low-address / sentinel guard, exactly as native
does).

---

## 4. Runtime-helper contract (per backend)

Replace the no-op stubs with real bodies. The contract mirrors
`arm64.go` (the cleanest native reference). Each must carry the guard
chain: **null → low-address (`<0x10000`, treats a non-pointer scalar
in an rc slot as "not a heap object") → SSO inline-tag (strings) →
static sentinel (high bit)**, before touching the rc word.

- `__fern_rc_inc(ptr) → ptr` — guards; `rc++`. (`arm64.go:1188`)
- `__fern_rc_dec(ptr)` — guards; underflow detector (rc≤0 bumps
  `__fern_rc_underflow`); `rc--`. **No free here** (Phase 1).
  (`arm64.go:1228`)
- `__fern_rc_is_unique(ptr) → i32` — 1 iff live & rc==1. The universal
  soundness second-net. (`arm64.go:1739`)
- `__fern_str_inc` / `__fern_str_dec` — two-word `(data,len)` string
  variants (only if the self-host adopts the two-word string ABI;
  otherwise the boxed-string path reuses `__fern_rc_*`).
- `__fern_box_free(data, size) → data` — free `base`, return data.
  (`arm64.go:998`)
- `__fern_arr_dec(data, stride)` — size-aware array buffer free at
  rc==1; does not walk elements. (`arm64.go:884`)
- `__fern_closure_drop(f) → f` — free rc1 block at rc==1, else dec.
  (`arm64.go:1032`)
- `__fern_alloc_rc1(size) → data` (rc=1 header),
  `__fern_alloc_box(size) → data` (sentinel header).
- `__fern_alloc_reuse(token, tokenSize, size) → ptr` — null token →
  `__fern_alloc`; class match → reuse; else free token + alloc.
  (`arm64.go:844`)
- Real allocator: per-size-class freelists + bump for large
  (`__fern_alloc` / `__fern_free`). Until Phase 3, `__fern_free` may
  stay a no-op (safe leak), so rc reaches 0 with no reclamation.

The wasm backend mirrors these in linear memory (see
`runtime.go:1199+` for `__fern_alloc`, `:1740` for dec) and needs a
needs-set dependency tracker like the native one (`EmitState.needed`
already exists — extend `mark_*`).

---

## 5. The Perceus analyses to port (shared, `asmcore.fern`)

Each is a pure function over the (monomorphised) AST + the type
oracle (`infer_expr_type`). Port in dependency order:

1. **`needs_rc_inc_on_alias(e, s) → bool`** ← `needsRcIncOnAlias`
   (`ir.go:14788`). Fires on Ident/FieldAccess/Index whose `Ty` is
   array/struct/enum/closure/tuple/string. The single inc trigger.
2. **`infer_param_escapes(module) → map`** ← `inferParamEscapes`
   (`ir.go:1336`). Greatest-fixpoint call-graph escape analysis;
   `param_borrowable(fn,i) = !escapes[fn][i]`.
3. **`compute_free_eligible(fn) → set`** ← `computeFreeEligible`
   (`ir.go:2574`). Taint analysis: borrowed params start tainted;
   locals tainted if they escape into a retain sink or alias a tainted
   local (fixpoint). Untainted rc locals + own params → free-eligible.
4. **`compute_consumed_params(fn) → set`** ← `computeConsumedParams`
   (`ir.go:14536`). Promote reassigned, drop-wired struct/tuple params
   to owned (the O(N²) self-reassign fix) + entry-inc.
5. **`compute_moved_locals(fn) → set`** ← `computeMovedLocals` +
   `markConstructionMoves` (`ir.go:3759/3903`). Move-on-return /
   -alias / -construction / -destructure pair cancellation, with the
   last-occurrence + dominance (top-level stmt, no preceding return)
   guards.
6. **`compute_precise_drops(fn) → map[stmtIdx → names]`** ←
   `computePreciseDrops` (`ir.go:3294`) + `safe_for_control_flow_drop`,
   `precise_droppable_type`, `flows_into_uncounted_alias`,
   `init_may_alias_live`. Last-use drop placement.
7. **`compute_reuse_sources(fn) → (map, set)`** ← `computeReuseSources`
   (`ir.go:12119`) + `reuse_class_of`. FBIP reuse-token pairing
   (dead owned box D ↔ same-class construction C).
8. **TRMC**: `find_trmc_funcs` ← `trmc.go`. Tail-recursion-modulo-cons,
   the consume-safe subset.

All eight are target-independent and depend only on the AST + `Ty`.
They produce per-function side-tables consumed by the per-backend
emit. Soundness rests on the **safe-leak invariant** (every
conservative bail degrades to dec-without-free, never over-release) and
the runtime **`__fern_rc_is_unique` second net**.

---

## 6. Phased rollout (each slice green + tested)

Mirrors the native plan; ordered so the tree (incl. the byte-identical
self-bootstrap) stays green between slices.

- **Phase 0 — layout + runtime.**
  - 0a: offset-helper refactor in `asmcore.fern`; route all backend
    offsets through it (no behaviour change). Gate: every self-host
    test + fixpoint still green.
  - 0b: rc-header layout migration, per category (array first), all
    allocs write `rc=1` (or sentinel for immortal). Still no inc/dec.
  - 0c: real runtime helpers (`__fern_rc_inc/dec/is_unique`,
    `__fern_box_free`, `__fern_arr_dec`, `__fern_alloc_rc1/box`) +
    underflow detector. `__fern_free` stays a no-op (safe leak).
- **Phase 1 — inc/dec everywhere (no Perceus, no free).**
  - 1d: arrays — inc at the 6 reference-creation sites, dec at exit /
    overwrite. Port `needs_rc_inc_on_alias` + `emit_alias_inc` +
    exit-dec sweep for `ArrayType`.
  - 1e: widen to string/struct/enum/closure/tuple + per-type drop
    handlers (`__drop_struct_*`, `__drop_enum_*`, `__drop_tuple_*`,
    `__fern_drop_arr_ptr`, closure drop thunk).
- **Phase 2 — rc check in mutating ops (CoW).** `arr.push` rc==1 fast
  path (the self-host already grows in place — add the rc gate),
  `arr[i]=v` CoW, Map set/delete/clear CoW + borrowed-param model.
- **Phase 3 — real allocator + free.** Size-class freelist; flip
  `__fern_free` + the rc==0 free path on, gated on a corpus-wide green
  underflow detector (drift audit first, exactly as native).
- **Phase 4 — Perceus pair-cancellation.** Move-on-return/alias/
  construction/destructure (port `compute_moved_locals`); precise
  drops (port `compute_precise_drops`, straight-line then
  control-flow-aware).
- **Phase 5 — drop reuse (FBIP) + borrowed params.** Reuse tokens
  (`compute_reuse_sources` + `__alloc_reuse`), consuming match
  (C1/C2), TRMC. Borrow inference (`infer_param_escapes`).
- **Phase 6 — reclamation polish.** Per-type reuse-path payload frees,
  nested/generic shapes, statement temporaries — the native Phase-6
  bullet list.

---

## 7. Test strategy

Mirror the native nets in the self-host's Go-side e2e harness
(`internal/e2e/self_host_*_test.go`, driven through `asm_run.fern` /
`asm_arm64_run.fern` / wasm):

- **Functional**: extend `TestSelfHostAsmRun{X86_64,Arm64,WASM}` with
  RC-exercising programs (alias + push loops, scope drops, reassign,
  match consume, struct/enum/closure builders) asserting exit
  code/stdout — value-correctness across the matrix.
- **Soundness**: an over-release counter (`__rc_underflow_count()`
  builtin, as native) asserted 0 on a clean corpus; deliberate
  double-dec asserted 1. Phase-3 go/no-go signal.
- **Memory**: peak-heap-bytes regression tests mirroring
  `internal/e2e/rc_heap_bump_*_test.go` once free is on — a push loop
  reclaims to O(1) blocks, precise drops bound the live set.
- **Determinism**: the byte-identical self-bootstrap fixed-point
  (`self_host_stage2_*_fixed_point_test.go`,
  `self_host_arm64_fixpoint_test.go`) and `TestSelfHostBootstrapsItself`
  must stay green at every slice — the analyses must be deterministic
  (no map-iteration-order dependence; use index-ordered walks like the
  native `markConstructionMoves` does).
- **Differential**: where practical, diff self-host output behaviour
  against the native compiler on the `rcCorpus` shapes.

Per CLAUDE.md: gate locally on x86-64 + wasm; let CI run the arm64 /
qemu matrix. Run the whole `internal/e2e` with `-timeout 30m`.

---

## 8. Invariants & risks

- **Safe-leak invariant** (from `OWNERSHIP-INFERENCE-PLAN.md`): every
  conservative bail degrades to dec-without-free, never over-release.
  A mis-analysis is a leak (slower), never a UAF — until Phase 3 turns
  free on, at which point the underflow detector + `is_unique` gate are
  the second net.
- **Determinism is mandatory.** The fixed-point test demands
  byte-identical output stage1↔stage2. Every analysis must walk the
  AST in a deterministic order and emit deterministically.
- **Two backends in lockstep.** Shared decisions in `asmcore.fern`
  mean an x86-green change is almost always arm64-green; the
  per-backend emit is the only place parity can break — keep the two
  `emit_*` edits mechanically identical.
- **Atomic layout slices.** A single missed offset breaks every
  program. Migrate one category at a time, route offsets through the
  shared helpers, and run the full functional suite per category.

---

## 9. Implementation log

(Updated as slices land.)

- 2026-06-07: design doc created; native + self-host fully mapped
  (this document).
- 2026-06-07: **Phase 0c (runtime layer), x86-64 — SHIPPED.** Replaced
  the no-op RC stubs in `asm.fern` with real bodies ported from the
  native x86_64 backend (`emitRcIncRuntime` / `emitRcDecRuntime` /
  `emitRcIsUniqueRuntime`): `__fern_rc_inc`, `__fern_rc_dec`,
  `__fern_rc_is_unique`, `__fern_rc_underflow_count`, plus the
  `__fern_rc_underflow` BSS counter. Guard chain adapted to the
  self-host BSS heap (null → SSO inline-tag → low-address `<0x10000` →
  static sentinel), replacing the native mmap-heap `<0x10000000`
  guard. No free path yet (safe-leak); `__fern_arr_dec` /
  `__fern_drop_arr_ptr` stay no-ops until the array layout migration.
  These are not yet wired into real allocations — they're exercised
  directly via `__alloc` + `__store_i32`/`__load_i32`. Tests:
  `TestSelfHostAsmRunX86_64/rc-*` (inc/dec arithmetic, is_unique
  true/false, underflow detected/clean, null-safe). Byte-identical
  self-bootstrap (`TestSelfHostStage2FixedPoint`,
  `TestSelfHostBootstrapsItself`, `TestSelfHostFixpoint`) stays green.
  Next: mirror to `asm_arm64.fern` + `wasm.fern`; then Phase 0b — the
  array rc-header layout migration that wires these in.
- 2026-06-07: **Phase 0c (runtime layer), arm64 — SHIPPED.** Mirrored
  the x86-64 helpers to `asm_arm64.fern` (ported from the native arm64
  backend: `ldur`/`stur` for `[ptr-8]`, `tbnz #31` sentinel, `tbnz #0`
  SSO tag, `mov x9,#0x10000` low-address guard, adrp/`:lo12:` access to
  the `__fern_rc_underflow` BSS counter). Tests consolidated into
  `internal/e2e/self_host_rc_runtime_test.go` with a shared case table
  driving both `TestSelfHostRcRuntimeX86_64` and
  `TestSelfHostRcRuntimeArm64` (the latter built + run under
  qemu-aarch64 — all six cases green). Next: wasm
  (`wasm.fern`); then Phase 0b array layout migration.
- 2026-06-07: **Phase 0a (array-alloc centralization), x86-64 —
  SHIPPED.** Introduced `__fern_arr_box(cap) -> data ptr` in `asm.fern`
  and routed all 11 array allocation sites through it (literal,
  str_split, the read-array builder, `__fern_alloc_u8`, `__fern_args`,
  push-grow, slice, concat, reverse, str_bytes, str_chars). The box
  layout (`[cap, len, e…]`, data = base+8, cap at `[data-8]`) is now
  defined in ONE place — behaviour-preserving (identical layout), so
  the full suite + byte-identical bootstrap stay green. The raw exec
  `argv` buffer (not a Fern array) is correctly left inline. This turns
  the rc-header migration (0b) into a ~2-line change to the helper +
  the single `cap` reader in `__fern_arr_push`. Clobbers only
  rax/rdi (matches `__fern_alloc`'s contract → drop-in at every site).
- 2026-06-07: **Phase 0b (array rc-header layout), x86-64 — SHIPPED.**
  Flipped `__fern_arr_box` to the rc layout: base = `[cap, rc, len,
  e…]`, data = base+16, so **rc sits at `[data-8]`** (the uniform offset
  the generic `__fern_rc_*` helpers read), cap at `[data-16]`, and every
  element/`len` offset relative to `data` is **unchanged** (array
  readers index off `data`, so only the alloc side moved). Allocs init
  `rc = 1`. The only non-helper edit was the single `cap` reader in
  `__fern_arr_push` (`-8`→`-16`). x86-64 arrays now carry live rc
  headers, ready for inc/dec wiring (Phase 1d). inc/dec are not emitted
  yet, so behaviour is unchanged (+8 bytes/array, underflow detector
  stays 0). Verified green: full asm-run + array / array-method /
  string / map / json / bytes / charmethods / map-iter/keys/literal
  self-host suites + the RC runtime tests + the byte-identical
  self-bootstrap (BootstrapsItself / Fixpoint / Stage2FixedPoint).
  Next: Phase 1d — emit `__fern_rc_inc` at array alias sites +
  `__fern_rc_dec` at scope/exit + overwrite (porting
  `needs_rc_inc_on_alias` + the exit-dec sweep). Then mirror 0a/0b/1d to
  arm64 + wasm.
- 2026-06-07: **Phase 1d-i (array alias-inc on var-decl), x86-64 —
  SHIPPED.** First RC counting wired into real allocations. Added the
  shared `needs_rc_inc_on_alias` + `ty_is_array_like` predicates to
  `asmcore.fern` (ported from native `needsRcIncOnAlias`, scoped to
  arrays), and emit `__fern_rc_inc` in `asm.fern`'s `StmtVar` general
  path when `var y = x` binds an array-typed ident alias (a fresh
  literal / call result is already owned at rc=1 and is NOT
  re-incremented). This is the first time the Phase-0c helpers run on
  the Phase-0b rc-headered arrays — proving the three layers integrate.
  Inc-only (no dec sites yet), so it's safe-leak + over-release-detector
  clean (rc only grows). Tests: `TestSelfHostRcAliasIncX86_64`
  (aliasing value-correctness, detector == 0, and an emission assertion
  that the retain is actually emitted at the alias). Byte-identical
  self-bootstrap + fixpoint stay green (the fixpoint executes the
  self-host's own array code with the new inc emission). NOTE: the
  user-level `xs = xs.push(v)` form on `i32[]` is a *pre-existing*
  self-host limitation (segfaults on origin/main too — `.push`
  reassignment lowers to a `-1` fallback), independent of this work; the
  self-host's internal push (validated via fixpoint) is unaffected.
  Next sub-slices: the remaining inc sites (reassign / call-arg /
  field+index alias / literal element / closure capture) and the dec
  sites (function-exit sweep + dec-on-overwrite) — together they
  balance the counts; then arm64 + wasm mirrors.
- 2026-06-07: **Phase 1d (reassign-inc + dec-on-overwrite), x86-64 —
  SHIPPED.** `asm.fern`'s `StmtAssign` now retains the new reference
  when `y = x` reassigns an array slot to an rc-tracked alias
  (`needs_rc_inc_on_alias`) and releases (dec) the OLD value the slot
  held (`ty_is_array_like` target). A fresh literal / call RHS is not
  re-incremented. Local, detector-clean for ordinary reassignment (the
  old value had rc>=1), and balances the reassignment lifecycle ahead
  of Phase 3. Tests: `TestSelfHostRcReassignX86_64` (reassign-to-alias /
  source-intact / reassign-to-fresh / no-underflow + an emission
  assertion that both the retain and the release are emitted). Full
  suite + byte-identical bootstrap/fixpoint green. KNOWN DRIFT (benign
  under safe-leak): the self-host's own `xs = xs.push(v)` internal form
  decrements a buffer the in-place push returned unchanged (the
  self-host push doesn't bump rc the way native Phase 2a does), so rc
  drifts down on repeated self-mutation. Harmless while free is off
  (values stay valid; fixpoint stays byte-identical); the cow-aware dec
  that fixes it is a Phase-3-prep item (mirrors the native drift audit).
  Next: the function-exit dec sweep (zero-init rc locals + per-return
  sweep, excluding borrowed params) — the other half of balance — then
  the remaining inc sites and the arm64/wasm mirrors.
- 2026-06-07: **Phase 1d (field/index alias inc), x86-64 — SHIPPED.**
  Extended the shared `needs_rc_inc_on_alias` to also fire on
  `ExprFieldAccess` / `ExprIndex` reads whose inferred type is an array
  (`var y = h.items`, `var y = m[i]` for an array-of-arrays), using
  `infer_expr_type`. Picked up automatically by both `StmtVar` and
  `StmtAssign` (no backend edits). Robust: a spurious inc on a
  mis-inferred non-array is harmless (the rc-inc guards short-circuit).
  inc-only → detector-clean. Tests: `alias-struct-field` +
  `emits-retain-at-field-alias`; struct / json / map self-host suites
  (which lean on field-access aliases) + bootstrap/fixpoint green. The
  alias-inc family now covers ident + field + index reads.
- 2026-06-07: **Phase 1d (function-exit dec sweep), x86-64 — SHIPPED.
  The array RC lifecycle is now BALANCED.** Added a per-function release
  sweep: at every in-function `return` and the fall-through exit,
  `emit_array_exit_dec` releases (dec) each array-typed LOCAL slot.
  Borrowed params are skipped via a new `n_params` boundary on
  `EmitState` (set by `emit_function` after binding params) — the borrow
  model (caller still owns the arg). Two supports make it sound under
  safe-leak: (1) `emit_function` zero-inits the body-local slots
  (`rep stosq`) so the sweep reads NULL (dec no-op) for a `var` skipped
  on the current path; (2) `StmtReturn` retains an array result before
  the sweep (and restores it after) so the returned buffer reaches the
  caller at unchanged rc. Combined with the alias/reassign incs +
  dec-on-overwrite, ordinary code is now over-release-detector clean.
  Tests: `TestSelfHostRcExitSweepX86_64` — return-array (retain past
  sweep), borrowed-param (not released, usable in caller), repeated-call
  detector == 0, not-taken-branch-local zero-init no-op, and an emission
  assertion (rep stosq + the dec sweep). Full self-host suite +
  byte-identical bootstrap/fixpoint green (the fixpoint executes the
  self-host's own functions through the new prologue zero-init + exit
  sweep). KNOWN benign drift persists for the self-host's internal
  `xs = xs.push(v)` (prior entry) — harmless while free is off; the
  cow-aware dec is the Phase-3-prep fix. With this the x86-64 array RC
  is functionally complete for Phase 1 (rc headers + all alias incs +
  reassign-inc + dec-on-overwrite + balanced exit sweep). Next: Phase 3
  (size-class freelist + flip free on, gated on a corpus-wide clean
  detector after the drift fix) and the arm64/wasm mirrors of 0a/0b/1d.
- 2026-06-07: **Phase 0a + 0b (array centralization + rc-header
  layout), arm64 — SHIPPED.** Mirrored the x86-64 array work to
  `asm_arm64.fern`: added `__fern_arr_box(x0=cap) -> data ptr` (rc
  layout — base `[cap, rc, len, e…]`, data = base+16, rc at `[data-8]`,
  cap at `[data-16]`; clobbers only x0/x1/x2 like the arm64
  `__fern_alloc`) and routed all 11 array allocation sites through it
  (literal, str_split, read-dir builder, `__fern_alloc_u8`,
  `__fern_args`, push grow, slice, concat, reverse, str_bytes,
  str_chars). The single `cap` reader in `__fern_arr_push` moved
  `[x19,#-8]` → `[x19,#-16]`. The raw exec `argv` buffer (not a Fern
  array) is left inline. arm64 arrays now carry live `rc=1` headers,
  matching x86-64's Phase-0b state. inc/dec not wired on arm64 yet
  (Phase 1d mirror is the follow-up), so behaviour is unchanged
  (+8 bytes/array). Verified under qemu-aarch64: the RC runtime, array,
  array-method, string, map, json, struct, closure, generics, bytes,
  tuple self-host suites + the byte-identical arm64 self-bootstrap
  (`TestSelfHostFixpointArm64` / `TestSelfHostStage2FixedPointArm64`).
  The shared `asmcore` analyses (needs_rc_inc_on_alias, ty_is_array_like,
  n_params) are already in place, so the arm64 1d wiring reuses them.
- 2026-06-07: **Phase 1d (array inc/dec wiring), arm64 — SHIPPED.
  arm64 array RC is now at full parity with x86-64.** Mirrored the
  Phase-1d emission into `asm_arm64.fern` (the shared `asmcore`
  analyses were already in place): alias retain in `StmtVar`,
  reassign-inc + dec-on-overwrite in `StmtAssign`, the function-exit
  release sweep (`emit_array_exit_dec`) at every return + fall-through,
  body-local zero-init (a small `str xzr` loop — arm64 has no
  `rep stosq`), `set_n_params` for the borrow boundary, and the
  array-return retain in `StmtReturn`. The arm64 rc-inc/dec call shape
  is `str x0,[sp,#-16]!; bl __fn___fern_rc_{inc,dec}; add sp,sp,#16`
  (frameless helper, arg at `[sp]`, ptr back in x0). Verified under
  qemu-aarch64: `TestSelfHostRcArm64` (alias / reassign / field-alias /
  return-array / borrowed-param / exit-sweep-no-underflow /
  branch-local-zeroinit) + the array / array-method / string / map /
  json / struct / closure / generics / tuple / bytes suites + the
  byte-identical arm64 self-bootstrap (`TestSelfHostFixpointArm64` /
  `TestSelfHostStage2FixedPointArm64`). Both production asm backends now
  have the complete balanced Phase-1 array RC lifecycle. Remaining:
  wasm backend (`wasm.fern`), Phase 1e (strings/structs/enums/closures/
  tuples), and Phase 3 (freelist + free, after the self-reassign drift
  fix) — the reclamation win.
- 2026-06-08: **Phase 3 prep (cow-aware dec-on-overwrite), x86-64 +
  arm64 — SHIPPED.** Fixed the self-reassign rc drift on both backends:
  `StmtAssign`'s dec-on-overwrite now SKIPS the release when the new
  value equals the old value the slot held (`cmpq;je` on x86-64,
  `cmp;b.eq` on arm64). This is the in-place-mutator case — e.g.
  `xs = xs.append(v)` growing within capacity returns the SAME buffer,
  which is still live, so releasing it would over-count. A genuine
  reassignment to a DIFFERENT buffer still releases the old. Measured:
  a 20-iteration `xs = xs.append(i)` loop went from 4 over-releases to
  **0** (the native drift audit's headline case). Tests:
  `TestSelfHostRcSelfMutateX86_64` (self-append-no-underflow / values /
  reassign-different-clean) + `TestSelfHostRcArm64` self-append cases
  (under qemu). Full suites + byte-identical bootstrap/fixpoint green on
  both backends. This removes the last known benign drift, so the
  over-release detector is now clean across ordinary code AND
  self-mutation — the go/no-go signal for flipping `free` on. Next:
  Phase 3 proper — a size-class freelist + flip `__fern_free` and the
  rc==1 free path on (array buffers reclaimed via __fern_arr_dec / the
  drop helpers), gated on the corpus-wide clean detector.
- 2026-06-08: **Phase 1d (struct-literal construction inc), x86-64 +
  arm64 — SHIPPED.** A struct field initialised from an rc-tracked array
  alias (`H { items: xs }`) now retains the buffer on both backends (the
  struct owns a new reference). This is the **free-readiness gate**: it
  closes the uncounted-alias hole that would become a use-after-free
  once free is on — a struct outliving the source local
  (`function mk(): H { var xs = [..]; return H{items: xs}; }`) keeps its
  array alive. inc-only (struct drop isn't wired — Phase 1e), so it's
  over-release-detector clean and a safe leak today. A fresh literal /
  call field value is owned and not re-incremented. Tests:
  `TestSelfHostRcConstructX86_64` (struct-holds-array,
  struct-outlives-source, struct-fresh-literal, emission) + green
  struct/json suites + byte-identical bootstrap/fixpoint on both
  backends.

  **Free-readiness gate (remaining uncounted-alias sites to close
  before flipping `free` on — each would be a UAF once buffers are
  reclaimed):** (a) the struct-UPDATE form `H{...base, f: xs}`;
  (b) array-literal elements `[xs, ys]` and tuple elements `(xs, n)`;
  (c) closure captures of an array; (d) index/field assignment
  `arr[i] = xs` / `obj.f = xs`. All are inc-only, detector-clean, safe
  while free is off. Once all are closed (and the corpus-wide detector
  stays 0 on the self-host itself), Phase 3 can flip free: a size-class
  freelist (`__fern_free` push + `__fern_alloc` pop, round-to-16 so
  classes are exact) and an array `__fern_arr_dec` that frees the buffer
  (size `(cap+3)*8`, base `data-16`) at rc==1 — routed from the exit
  sweep + dec-on-overwrite. Pointer-element arrays may free their buffer
  without an element walk (sound: elements leak, never UAF) until a
  drop-walk slice lands.
- 2026-06-08: **Phase 1d (array + tuple construction inc), x86-64 +
  arm64 — SHIPPED.** Array-literal elements (`[a, b]`) and tuple-literal
  elements (`(xs, n)`) initialised from an rc-tracked array alias now
  retain the buffer on both backends (the container owns the reference),
  with the container pointer preserved across the retain call. Closes
  two more free-readiness gate sites. inc-only / detector-clean / safe
  (free off). Tests: `TestSelfHostRcConstructContainersX86_64`
  (array-of-arrays, tuple-of-array, return-arr-of-arrs — the
  would-be-UAF capture case) + arm64 cases in `TestSelfHostRcArm64` +
  green tuple/array suites + byte-identical bootstrap/fixpoint on both
  backends. Free-readiness gate now CLOSED for: struct-literal /
  array-literal / tuple-literal element stores. Remaining before the
  free flip: struct-UPDATE form `H{...base, f: xs}`, closure captures of
  an array, and index/field assignment `arr[i] = xs` / `obj.f = xs`.
- 2026-06-08: **Phase 1d (struct-update construction inc), x86-64 +
  arm64 — SHIPPED.** The struct-update form `H { ...base, f: v }` now
  retains BOTH the copied non-overridden array fields (the new struct
  references the base's arrays) AND override array values, on both
  backends (base + box ptrs preserved across the retain calls). This is
  the heavy self-host path (`EmitState { ...s, … }` is threaded
  everywhere and carries many array fields), so it's the most important
  remaining gate site — and matches the native IR lowering, which inc's
  copied pointer fields. Tests: `struct-update-copy` /
  `struct-update-override` in `TestSelfHostRcConstructX86_64` +
  `TestSelfHostRcArm64` + green struct-update / functional-update suites
  + byte-identical bootstrap/fixpoint (the fixpoint exercises the
  EmitState-spread path heavily). Free-readiness gate now CLOSED for:
  struct-literal / array-literal / tuple-literal element stores AND the
  struct-update form. Remaining before the free flip: closure captures
  of an array, and index/field assignment `arr[i] = xs` / `obj.f = xs`.
- 2026-06-08: **Phase 1d (closure-capture construction inc), x86-64 +
  arm64 — SHIPPED.** A lambda capturing an rc-tracked array now retains
  the buffer at the capture-store (the closure box owns the reference),
  on both backends (box ptr preserved across the retain call). Uses the
  block-form lambda `function (): T { … }` (the working closure shape —
  the arrow form `() => e` capturing a local is a *separate pre-existing*
  self-host limitation: it segfaults on origin/main regardless of RC).
  Tests: `TestSelfHostRcClosureX86_64` (local closure + escaping closure
  capturing an array, both detector-0 + value-correct) + green
  closures/higher-order suites + byte-identical bootstrap/fixpoint on
  both backends. Free-readiness gate now CLOSED for: all literal
  constructions, the struct-update form, AND closure captures. **Only
  remaining site: index/field assignment** (`arr[i] = xs` / `obj.f = xs`
  storing an array) — note `obj.f = …` struct-field assignment is
  rejected by the checker (E048-style), and array-of-arrays index-assign
  is rare; once confirmed/closed, Phase 3 can flip free.
- 2026-06-08: **Phase 3 (free flip) — ATTEMPTED, NOT LANDED (gated on a
  residual under-count).** Implemented the x86-64 size-class freelist
  (`__fern_alloc` round-to-8 + freelist pop; `__fern_freelist` BSS, 1024
  classes) and the real `__fern_arr_dec` (frees the buffer at rc==1:
  base `data-16`, size `(cap+3)*8`, idx `cap+3`), and rerouted the
  exit-sweep + dec-on-overwrite array releases through it. Bounded probes
  all passed (churn-loop reclaim, alias, reassign, return-array,
  index-assign, match-binding, for-over-arr-of-arr, nested-call-temp —
  all value-correct, detector 0). **But the full self-host
  (`TestSelfHostStage2FixedPoint`, mmc2 compiling itself) SEGFAULTED** —
  a double-free: some array buffer in the self-host's own code has two
  live references while its rc reflects one, so the second release
  re-frees a recycled block. Zeroing the rc on free (so a stray second
  release reads 0 → detector, not re-free) made the fixpoint pass, which
  CONFIRMS the diagnosis (double-free, not a missing helper) but only
  MASKS it — if the freed block is reused between the two releases, the
  second release corrupts the live reuser, so it is not sound. The Phase-3
  changes were reverted; the branch stays at the sound free-off state.
  **Before free can flip:** find + fix the residual under-count. The
  prime suspect is an uncounted alias from a CALL result that aliases an
  argument (e.g. `var z = f(xs)` where `f` returns its param), which the
  current `needs_rc_inc_on_alias` (ident/field/index only) does not
  retain — this is exactly what the native compiler's
  `inferParamEscapes` / `findReturnsNoParamEscape` analyses handle. The
  port's next slice is that escape/alias analysis (or a precise audit of
  the self-host's array-returning helpers), after which the freelist +
  `__fern_arr_dec` flip above can be re-applied and validated against the
  bootstrap/fixpoint + a corpus-wide-0 detector.
