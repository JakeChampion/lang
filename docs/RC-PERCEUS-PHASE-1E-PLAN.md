# RC + Perceus Phase 1e plan

Implementation plan for widening refcount tracking from arrays
(Phase 1d, SHIPPED) to user structs, strings, enums, and
closures (Phase 1e).

Date: 2026-05-21.
Status: design, draft executing.

## Why this plan exists

The parent doc `docs/RC-PERCEUS-PLAN.md` sketches Phase 1e in
one paragraph:

> Each non-array type category needs: Layout migration
> adding the rc slot. rc=1 init at every alloc site. inc
> emissions at every alias site. dec emissions at function
> exit + reassignment overwrite.

The first attempt at executing that sketch — widening the
IR-side predicates (`needsRcIncOnAlias`,
`emitRcDecLocalsAtExit`, zero-init, `isRcTrackedLocal`) to
also accept `ast.StructType` — segfaulted the
`TestX86_64ReaderWriter/streaming_roundtrip_lines` e2e test.

Root cause: every `*ast.StructDecl` registered in
`info.Structs` looks identical to the IR layer, but only some
are heap-allocated by user-side `StructLit` syntax. The rest
are *runtime opaque structs* — Reader, Writer, Map, MapIter,
Stream, BytesWriter, HttpRequest, HttpResponse, FileStat,
Span, ProcessResult, TestRunner, MockPlatform, Platform,
TimeZone, Url, HeaderMap, MockCall, `__JsonParser` — all
declared in `internal/checker/checker.go:builtinStructDecls`
or its equivalents and allocated by hand-rolled assembly in
`internal/codegen/{arm64,x86_64,wasmbin}`.

Widening the predicates blindly causes `__fern_rc_inc/dec`
to read `[data - 8]` on a runtime-allocated struct whose
allocation slot has no rc word. The dec helper writes garbage
to whatever happens to live there, corrupting heap state and
crashing later loads.

The fix is to give every struct-shaped heap value an rc word
at `data - 8`, regardless of who allocated it. Runtime
helpers that don't want rc semantics (i.e. don't want
`mutate-in-place` to ever fire) write the static-sentinel
high bit `0x80000000` — `__fern_rc_inc/dec` short-circuit on
that bit, so the struct behaves exactly as it does today.

## The sentinel approach

`__fern_rc_inc/dec(ptr)` already short-circuit when the high
bit of `[ptr - 8]` is set:

```
ldur w1, [x0, #-8]
tbnz w1, #31, .Lrcinc_ret
```

The empty-array sentinel uses this: a static, never-allocated
heap value the runtime hands to `[]i32`-typed zero-elem
expressions, with `[ptr - 8] = 0x80000000` so any rc inc/dec
is a no-op. The cap / len / etc. live at the usual offsets.

Phase 1e applies the same pattern to runtime-allocated
structs:

  - Bump every runtime struct alloc by 8 bytes (`size + 8`).
  - Write `0x80000000` at `[base + 0]`.
  - Return `base + 8` as the "data" pointer.
  - All existing field accesses use offsets relative to
    data, unchanged.

After this, the IR-side widening of `needsRcIncOnAlias` etc.
can fire safely on any struct in `info.Structs` — the inc/dec
either bumps a real rc (for user `StructLit`-allocated values)
or no-ops on the sentinel (for runtime-allocated values).

User structs that genuinely want rc tracking get `rc=1`
written at `[base + 0]` by IR-side `StructLit` lowering
instead of the sentinel, picking up the eventual Phase 2
mutate-in-place / drop-reuse semantics.

## Phased rollout

### Phase 1e-runtime: sentinel-pad every runtime struct alloc (SHIPPED across PRs #1237, #1238, #1242)

First slice. Goal: every runtime helper that returns a
struct-shaped pointer writes the static sentinel at
`[base + 0]` and returns `base + 8`. Per-backend (arm64,
x86_64, wasmbin) since the helpers live in each backend's
runtime emitter.

Helpers to migrate (grouped roughly; exact list per backend
to be confirmed during execution):

  - ✅ **Reader / Writer** (PR #1237): `__fern_make_handle`,
    `__fern_open_reader`, `__fern_open_writer`,
    `__fern_open_appender` (and the `wasmbin/wasi_fs.go`
    Reader/Writer struct construction).
  - ✅ **Map / MapIter** (PR #1238): `map_new_impl`'s
    handle allocation + `__map_iter_impl`.
  - ✅ **HttpRequest / HeaderMap** (folded into PR #1242):
    the wasi-http runtime's struct construction.
  - **Stream / BytesWriter** — both are user-side
    `StructLit`, so they migrated naturally with Phase
    1e-struct-i (PR #1239).
  - **HttpResponse / Url / TimeZone / Zoned / Span /
    Duration / Instant / Time / Date / DateTime / etc.** —
    all user-side `StructLit`, migrated with Phase 1e-struct-i.
  - **FileStat / ProcessResult / MockCall / TestRunner /
    Platform / MockPlatform / __JsonParser** — user-side
    `StructLit`, migrated with Phase 1e-struct-i.

After this slice ships, no semantic change is visible to
user code — the IR predicates still gate on `ArrayType`
only. The rc inc/dec helpers are simply safe to invoke on
struct values, whether they reach a real rc word or a
sentinel.

Tests: existing e2e suite must stay green (no behavior
change is the contract).

### Phase 1e-struct-i: layout migration in IR StructLit (SHIPPED, PR #1239)

Add an 8-byte header to user `StructLit`-allocated values:

  - `OpConstI32(size + 8); OpAlloc;` (instead of
    `OpConstI32(size); OpAlloc;`).
  - Write `rc = 1` at `[base + 0]`.
  - The IR-side baseSlot holds `base + 8` (= data) so the
    existing per-field stores at `[baseSlot + offs[name]]`
    continue to work unchanged. Push `base + 8` at the end
    of the `StructLit` case to leave the user-visible
    pointer on the stack.

Slot accounting note: the first attempt at this stored
`base` in baseSlot and computed `base + 8` lazily before
each field access, which made the SSA lift's slot-resolution
unhappy at one prelude function (an Instant struct allocated
inside a conditional branch). Sticking to a single slot per
StructLit but shifting field offsets by 8 (`OpConstI32(8 +
off)` instead of `OpConstI32(off)`) keeps the slot count
identical to today and lets the SSA lift continue working
unchanged.

After this slice, user structs allocated via `StructLit`
carry an rc word but the IR predicates still don't fire on
them, so no inc/dec activity. Tests stay green.

### Phase 1e-struct-ii: widen `needsRcIncOnAlias` (SHIPPED, PR #1242)

Accept `ast.StructType` whose name is in `info.Structs`.
Inc emissions fire at the same alias sites as the array
equivalents:

  - `var y = x;` (Phase 1d-i analogue).
  - `var y = h.items;` / `var y = m[i];` (Phase 1d-ii).
  - `y = x;` ident reassignment (Phase 1d-iii).
  - `f(arr)` call-arg pass (Phase 1d-iv).
  - Closure capture (Phase 1d-vii).
  - Lit-element / field-init (Phase 1d-viii).

All six existing inc sites consult the same predicate, so
this is a one-line change. Phase 1e-runtime must have
shipped first so the sentinel exists on runtime structs;
otherwise an inc on `var w = open_writer(...)`'s result
corrupts memory.

### Phase 1e-struct-iii: widen `emitRcDecLocalsAtExit` + zero-init (SHIPPED, PR #1244)

Mirror the array exit-dec sweep for struct-typed locals:

  - Dec every struct-typed param at every function-exit path.
  - Dec every struct-typed local at every function-exit path.
  - Zero-init every struct-typed local at function entry so
    the dec helper's NULL short-circuit fires on slots whose
    Var was never reached.

Same widening shape as Phase 1d-v: gate on the rc-tracked
type set (`ArrayType` plus user-declared `StructType`).

### Phase 1e-struct-iv: widen `isRcTrackedLocal` (dec-on-overwrite, in this PR)

`y = x;` must dec `y`'s old value before storing `x`. The
predicate already mirrors `needsRcIncOnAlias`; widening it to
accept struct types matches the inc side.

### Phase 1e-enums-i: rc header in `emitEnumNew` (SHIPPED)

Variant boxes grow the 8-byte rc header (rc=1 at `[base+0]`,
data = `base+8`). Same layout migration as struct-i.

### Phase 1e-enums-runtime: sentinel runtime enum boxes (SHIPPED, arm64 + x86_64 + PR #1259 wasmbin)

`__fern_alloc_box(size)→data` on every backend; every
runtime-built Option / Result / IoError box routes through it
so the static sentinel `0x80000000` sits at `[data-8]`.

### Phase 1e-enums-ii: widen the rc predicates to `EnumType` (this PR)

Not a one-line widening — three coordinated changes, because an
enum-typed local can hold *three* pointer shapes and all of them
must survive `__fern_rc_inc/dec`:

  1. **Headered heap box** from `emitEnumNew` — already safe
     (enums-i).
  2. **Pair-form rebox.** `emitRepackPairAsHeapBox` (the
     call-site repack of an `OpCallDirectPair` result that flows
     into a var / field instead of straight into a `match`) built
     a *headerless* box. Migrated to the same `rcHeaderBytes`
     layout as `emitEnumNew`. The transient `(tag, payload)` pair
     never lands in a slot — only on the operand stack between a
     pair call and its match dispatch — so the dec sweep never
     sees a non-pointer enum value.
  3. **Static nullary sentinel.** `OpEnumSentinel` (None,
     `IoError.Interrupted`, `JNull`, any payloadless variant)
     lowered to a bare `.4byte tag` in `.rodata` (natives) / the
     data segment (wasm) with **no** preceding rc word. Dec'ing
     such a value would read `[ptr-8]` (arbitrary `.rodata`) and,
     absent the high bit, *write* `rc-1` back → segfault on the
     read-only section. Fixed by prepending the 8-byte
     `0x80000000` header to every sentinel cell in all three
     backends. `OpMatchTag`'s `[ptr+0]` read is unchanged.

With those three in place the four predicates (`rcTracked` /
`zeroRcTracked` exit sweep, `isArrayTypeOfLocal` dec-on-
overwrite, `needsRcIncOnAlias`) accept `EnumType` directly — no
pair-form exclusion needed, since slots are always pointer-
shaped. No functional payoff until Phase 3 frees at rc=0; this
slice is the safety groundwork so enum values can be tracked
without corruption.

### Phase 1e-closures-i: rc-header layout for closures (this PR)

A `FuncType` local holds one of: a heap closure pair
`{fn, env}` (`OpMakeClosure`), a heap env block (`OpMakeEnv`),
a static `{fn, env=0}` cell (`OpConstFunc`), or null. The heap
allocations move from raw `__fern_alloc` to a new
`__fern_alloc_rc1(size)→base+8` helper (mirrors
`__fern_alloc_box` but writes a live `rc=1` instead of the
immortal sentinel, so closures are droppable in Phase 3). The
static `OpConstFunc` cells gain the immortal `0x80000000`
header on the natives (`.rodata`); the wasm cells live in the
sub-64-KiB reserved window and are handled by the rc helpers'
low-address guard in -ii. Because each pointer keeps pointing at
the data view (`base+8`), all intra-env capture offsets and
intra-pair fn/env offsets are UNCHANGED — the shift lives only
at the alloc sites. Behavior-neutral; full suite green.

### Phase 1e-closures-ii: widen the predicates to FuncType (SHIPPED)

Widening `rcTracked` / `zeroRcTracked` / `isArrayTypeOfLocal` /
`needsRcIncOnAlias` to `*ast.FuncType` was NOT a drop-in like
enums: it fought two closure optimization passes that run in the
backend after rc insertion. Both `Defunctionalise` /
`returnTargetFor` and `ElideClosurePair` were made rc-aware:
they ignore the zero-init (`const 0`) writer, treat
`OpCallDirect __fern_rc_inc` as a value-preserving pass-through
when chasing alias chains, skip the benign exit
`OpLoadLocal; __fern_rc_dec` reader, and skip the exit dec-sweep
triples when locating a function's returned value. wasm gained
the `rc_inc` low-address guard. Original interaction notes:
  - **Zero-init poisons defunctionalisation.** The zero-init
    store (`OpConstI32 0; OpStoreLocal slot`) gives every closure
    slot a second, non-`OpMakeClosure` writer, so
    `Defunctionalise` / `ElideClosurePair` mark the slot
    polymorphic and bail — `OpCallIndirect` is no longer
    rewritten to `OpCallClosureDirect`, and zero-capture closures
    stop eliding their alloc. Fix: teach the passes to ignore a
    `const-0` writer when classifying closure slots.
  - **`rc_inc` breaks alias chains.** An alias `b = a` lowers to
    `OpLoadLocal a; OpCallDirect __fern_rc_inc; OpStoreLocal b`;
    the mono-slot chaser must see `__fern_rc_inc` as a
    value-preserving pass-through (it returns its argument) to
    keep propagating the target through the hop.
  - wasm needs the `rc_inc` low-address guard (mirror `rc_dec`)
    so the sub-64-KiB static cells short-circuit.
The layout (-i) is shipped and inert; do -ii only with the two
optimizer fixes in the same PR, gated on the defunc/elide tests.

### Phase 1e-strings

Same pattern after structs land. Strings have the extra
wrinkle of SSO (inline vs. heap) — only the heap case grows
an rc header. Enums use `emitEnumNew` which allocates `Ok` /
`Err` / variant boxes; the layout migration goes there. The
runtime-helper migration in Phase 1e-runtime should anticipate
this and handle the enum / closure runtime helpers in the same
sweep.

## Risk

  - **Cross-backend coverage.** Each runtime helper exists in
    three places (arm64, x86_64, wasmbin). The migration must
    cover all three or one backend's tests will break.
  - **Heap address calculations elsewhere.** Anywhere in the
    runtime that compares two struct pointers for equality
    will still get a deterministic answer (both shift by 8),
    but any code that depends on the *exact* pointer being an
    allocator-returned base will break. Worth auditing for
    "raw alloc pointer" usage during execution.
  - **Drop-handler placeholder.** Phase 3's freelist returns
    storage starting from `base`, not `data`. Make sure the
    `data - 8` convention survives the eventual free, e.g. by
    having the dec helper compute `base = data - <header>`
    consistently.

## Estimated effort

  - Phase 1e-runtime: 1–2 PRs per backend (3 backends total),
    plus a sanity-check PR that proves the streaming /
    process-result / map e2e suites pass unchanged.
  - Phase 1e-struct-i through 1e-struct-iv: one PR each, ~50
    LOC.
  - Phase 1e-strings / enums / closures: 1–2 PRs each,
    depending on how cleanly the runtime migration generalises.

Total: ~10–15 PRs across the slice. Each is small and
reviewable; the contract for every PR is "existing tests
unchanged, new tests prove the new alias / dec path works".

## Reference

  - `docs/RC-PERCEUS-PLAN.md` — parent plan.
  - `internal/codegen/arm64/arm64.go:emitRcIncRuntime` /
    `emitRcDecRuntime` — the rc helper assembly with the
    sentinel short-circuit.
  - `internal/ir/ir.go:emitRcDecLocalsAtExit` — Phase 1d-v
    array dec sweep (template for the struct version).
  - `internal/ir/ir.go:needsRcIncOnAlias` — the predicate
    everyone shares.
