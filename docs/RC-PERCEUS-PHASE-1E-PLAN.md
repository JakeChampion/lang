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

Widening the predicates blindly causes `__lang_rc_inc/dec`
to read `[data - 8]` on a runtime-allocated struct whose
allocation slot has no rc word. The dec helper writes garbage
to whatever happens to live there, corrupting heap state and
crashing later loads.

The fix is to give every struct-shaped heap value an rc word
at `data - 8`, regardless of who allocated it. Runtime
helpers that don't want rc semantics (i.e. don't want
`mutate-in-place` to ever fire) write the static-sentinel
high bit `0x80000000` — `__lang_rc_inc/dec` short-circuit on
that bit, so the struct behaves exactly as it does today.

## The sentinel approach

`__lang_rc_inc/dec(ptr)` already short-circuit when the high
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

### Phase 1e-runtime: sentinel-pad every runtime struct alloc

NOT YET STARTED — first slice. Goal: every runtime helper
that returns a struct-shaped pointer writes the static
sentinel at `[base + 0]` and returns `base + 8`. Per-backend
(arm64, x86_64, wasmbin) since the helpers live in each
backend's runtime emitter.

Helpers to migrate (grouped roughly; exact list per backend
to be confirmed during execution):

  - **Reader / Writer**: `__lang_make_handle`,
    `__lang_open_reader`, `__lang_open_writer`,
    `__lang_open_appender`, `__lang_stdin`, `__lang_stdout`,
    `__lang_stderr` (and their `__lang_close_fd_box` helper).
  - **Map / MapIter**: `__lang_map_new`, `map_new_impl`'s
    handle/buf allocations, `__lang_map_iter_new`.
  - **Stream / BytesWriter**: `__lang_stream_*`,
    `__lang_bytes_writer_*`.
  - **HTTP**: `__lang_http_request_*`, `__lang_http_response_*`.
  - **FileStat / Span / ProcessResult**: dedicated alloc
    helpers in `wasi_misc.go` / `arm64.go` / `x86_64.go`.
  - **TestRunner / MockPlatform / Platform**:
    `__lang_test_runner_*`, `__lang_mock_platform_*`,
    `__lang_platform_*`.
  - **Misc**: `__lang_url_*`, `__lang_header_map_*`,
    `__lang_time_zone_*`, `__lang_zoned_*`.

After this slice ships, no semantic change is visible to
user code — the IR predicates still gate on `ArrayType`
only. The rc inc/dec helpers are simply safe to invoke on
struct values, whether they reach a real rc word or a
sentinel.

Tests: existing e2e suite must stay green (no behavior
change is the contract).

### Phase 1e-struct-i: layout migration in IR StructLit

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

### Phase 1e-struct-ii: widen `needsRcIncOnAlias`

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

### Phase 1e-struct-iii: widen `emitRcDecLocalsAtExit` + zero-init

Mirror the array exit-dec sweep for struct-typed locals:

  - Dec every struct-typed param at every function-exit path.
  - Dec every struct-typed local at every function-exit path.
  - Zero-init every struct-typed local at function entry so
    the dec helper's NULL short-circuit fires on slots whose
    Var was never reached.

Same widening shape as Phase 1d-v: gate on the rc-tracked
type set (`ArrayType` plus user-declared `StructType`).

### Phase 1e-struct-iv: widen `isRcTrackedLocal` (dec-on-overwrite)

`y = x;` must dec `y`'s old value before storing `x`. The
predicate already mirrors `needsRcIncOnAlias`; widening it to
accept struct types matches the inc side.

### Phase 1e-strings / enums / closures

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
