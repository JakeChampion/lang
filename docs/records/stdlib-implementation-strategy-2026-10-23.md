# Original standard-library implementation strategy

Status: [record] source text preserved from the earlier language direction.

This record cites historical source paths. It describes the retired Go
emitters and the auto-injected prelude. The current module system and compiler
use different paths.

The quotation below comes from `docs/LANGUAGE-DIRECTION.md` at commit
`8547531b2a3464b0e086527d76668a47e468f948`.

> ## Stdlib implementation strategy: hand-written wat vs IR-routed
>
> Two flavours of stdlib helper coexist:
>
> 1. **Runtime core (hand-written wat).** `__fern_alloc`,
>    `__str_eq`, `__str_slice`, `__slice_idx_*`, the Map's hash
>    mix + open-addressing core, `arena_save` / `arena_restore`,
>    the int formatter, and the wasi imports. Hand-written wat
>    in `internal/codegen/wasm/wasm.go`. The IR sees only the
>    call sites (`OpCallDirect "name"`); helper bodies bypass
>    the IR entirely.
>
> 2. **Fern prelude (IR-routed).** `internal/prelude/prelude.fern`
>    — a small embedded source file parsed at checker startup
>    and prepended to the user's program. Goes through the
>    regular parser → checker → IR → codegen pipeline like any
>    user code, so it picks up IR-level optimisations (peephole,
>    dce, future inlining) and works on any backend without
>    backend-specific shims. Method receivers in the prelude
>    can be built-in scalar types (`function (s: string)
>    is_empty(): boolean { return len(s) == 0; }`) and the
>    receiver-hoisting + dispatch path treats them the same
>    way as struct/enum methods.
>
> Migration is incremental: each high-level helper moves from
> wat to prelude on its own; the runtime core stays in wat
> because the operations needed (memory.copy, memory.grow, raw
> loads/stores, hash mixing) aren't expressible at the Fern
> level today.
>
> **Performance comparison** (rough, based on the existing
> helpers):
>
> | Aspect                          | Hand-written wat   | IR-routed Fern    |
> |---------------------------------|--------------------|-------------------|
> | Tight loops (memcpy, scan, hash) | optimal           | within 10%, maybe |
> | Tiny helpers (`s.is_empty()`)    | direct one-op     | call overhead     |
> | Recursive walkers (json encoder) | one alloc per buf | match-arm + heap  |
> | IR optimisations (peephole, dce) | invisible         | applied           |
> | Cross-target (wasm + arm64)      | duplicate per backend | one source    |
> | Maintainability                  | verbose, fiddly   | concise           |
>
> The wins from IR-routing are **maintainability + cross-target
> portability**, not raw performance. For most helpers the
> generated code is comparable; for hot paths (the Map runtime,
> hash mixing, the bump allocator) hand-written wat is meaningfully
> faster because we control the exact ops without going through
> the Fern's abstraction layers.
>
> **Migration path** (split into per-helper PRs):
>
> 1. ✅ **Phase A: prelude infrastructure + first migration.**
>    `internal/prelude/prelude.go` embeds the `prelude.fern`
>    source via `//go:embed`; `injectPrelude` parses it on
>    each `checker.Check` call and appends the decls to
>    `prog.Funcs` (with `IsPrelude=true` for filtering in
>    tests / dump tools). Method-receiver type-checking
>    extended to accept `string`, `i8/i16/i32/i64/u8/u16/u32/u64`,
>    and `f32/f64` receivers — built-in scalar receivers go
>    through the same hoisting + dispatch as struct / enum
>    methods. First migration: `s.is_empty()` — was 11 lines
>    of hand-written wat, now 3 lines of Fern.
> 2. **Phase B: migrate higher-level stdlib.** `parse_int`,
>    `parse_float`, `s.repeat`, `url_encode`, `url_decode`,
>    `query_parse`, `url_parse`, `json_encode`, `json_parse`,
>    `f32.to_string`, `f64.to_string` — all shipped on a
>    per-PR cadence. Phase B continued with the string
>    methods: `starts_with`, `ends_with`, `contains`,
>    `index_of`, `trim` (slice-based, no allocation), then
>    `to_lower`, `to_upper`, `bytes` (allocation-based,
>    built on the new `__alloc_u8` primitive), then `split`
>    and `replace` (variable-length result, built on
>    `__array_append_string` + concat). The supporting
>    `__is_ascii_ws` and `__bytes_eq` wat helpers retired
>    together with the migrations — every string method
>    that has a wat helper has now moved, except
>    `as_bytes` (which still needs slice-header alloc the
>    prelude doesn't yet expose).
> 3. **Bridge functions for wasm intrinsics shipped.**
>    `__memcpy(dst, src, n)`, `__memset(dst, b, n)`, and
>    `__alloc_u8(n): u8[]` are thin wat-shim wrappers around
>    wasm's bulk-memory `memory.copy` / `memory.fill` plus a
>    tiny `__fern_alloc` + length-prefix sealer. Signatures
>    registered in the checker so the prelude can call them
>    like any builtin. Drives buffer-management code that
>    doesn't yet have a clean Fern-level shape — the
>    remaining wat string methods (`to_lower`, `to_upper`,
>    `bytes`) migrated using these primitives, then the Map
>    runtime followed (~1184 wat lines → ~280 Fern lines).
>    The map's `__method_Map_*` calls keep their type-rich
>    FuncSigs registrations (the language doesn't yet have
>    generic methods on a generic struct), with a codegen
>    alias that rewrites each call to its concrete `_impl`
>    counterpart in the prelude — same pattern as the
>    `__array_append_jsonvalue → __array_append_string`
>    alias. Native backends provide `__memcpy` / `__memset` /
>    `__alloc` directly (arm64 inlines them as small leaf
>    functions; wasm wraps `memory.copy` / `memory.fill`).
>
>    Drive-by escape hatch: `[u8]` slice and `u8[]` owned
>    array now cast to `i32` to recover the data pointer.
>    Slice cast loads the i32 at slice+0 (the data_ptr field
>    of the `{data_ptr, len}` slice header); array cast is a
>    no-op since an owned-array value already IS the data
>    pointer (length lives at data-4). Together with the
>    bulk-memory shims this is enough to express growable-
>    buffer scratch code in Fern without dropping into wat.
>
> **Why not migrate everything at once:**
>
> - Fern is missing some primitives the prelude would want
>   for the most aggressive helpers: `i32 ↔ f32` bit-cast (for
>   the float formatter's exponent bit pattern), `memory.copy`
>   shim (for the json buffer). Add these incrementally.
> - The IR inliner now handles small functions with internal
>   control flow + non-recursive direct calls (extended in
>   the post-Map-runtime cleanup so the migrated prelude's
>   prefix / scan / hash helpers actually inline). Bodies up
>   to 80 ops with internal `if` / `while` / `return` substitute
>   cleanly; the wrapper-block + br-translation shape lets
>   early returns fall through to the caller's continuation.
>   Recursion + closure-call ops still disqualify. Compounds
>   for every hot prelude helper — `__substr_eq`, `__map_hash`,
>   the small char classifiers — that previously paid full
>   call overhead per use.
> - Each migration removes per-PR risk: the migrated helper is
>   validated against the existing test suite (no behavior
>   change), so the wat → Fern transition is observable only
>   in the wasm size + readability of the source, not in the
>   user-visible behavior.
>
