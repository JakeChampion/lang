# Arena: keep-or-remove decision

**Status:** arena REMOVED in full — the `arena { … }` block syntax, the
`arena_save` / `arena_restore` builtins, and their arm64 / x86-64 / wasm runtime
helpers (decided 2026-06-01). Per-request memory is now reclaimed by reference
counting (RC) only.

**Owner:** compiler / runtime.

## What "arena" was

The language carried an `arena` feature in two layers:

1. **The `arena { … }` block statement** — a reserved keyword and an `ast.Arena`
   node that desugared to `arena_save() → body → arena_restore(saved)`. Never
   used by any `.fern` source.

2. **The `arena_save()` / `arena_restore(handle)` builtins** plus their runtime
   helpers (`__fern_arena_save` / `__fern_arena_restore`) — leaf functions that
   snapshot and rewind the bump-allocator cursor. These *were* used:
   - `std/tcp.fern`'s `tcp_serve` loop bracketed each request in
     `arena_save()` … `arena_restore(saved)` for per-request reclaim.
   - The wasm `__http_entry` wrapper (`wasi_http.go`) did the same per request.

## Decision: remove everything, rely on RC

Both layers are gone. `tcp_serve` and `__http_entry` no longer reset the heap
between requests; per-request allocations are freed by reference counting as
their last references drop at the end of each iteration.

This was a deliberate, eyes-open call. The trade-off was made explicit before
removal (see "Consequence" below) and accepted.

### What was removed

- `arena` keyword, `ast.Arena` node + its statement-walk cases, `parseArena`
  (the block syntax — removed in the prior change).
- `arena_save` / `arena_restore` checker `FuncSigs` and interp builtins.
- Native runtime: x86-64 / arm64 `emitArenaRuntime`, the `usesArena` flag, and
  the call-name dispatch.
- Wasm runtime: `__fern_arena_save` / `__fern_arena_restore` helper bodies,
  their dependency-resolver and force-memory hooks, and the user-call name
  mapping. The `__http_entry` wrapper's two call sites were removed; its local
  slot 24 (formerly `$arena_handle`) is left allocated-but-unused to avoid
  renumbering slots 25..43.
- `std/tcp.fern`'s `arena_save()` / `arena_restore()` calls.
- The dedicated tests (`TestWASMArenaReset`, `TestArm64Arena`, `TestX86_64Arena`,
  the arm64-darwin `arena` syscall-table case, and the wasm-runtime unit test
  `TestEmitArenaSaveRestore`). Two closure no-alloc e2e tests that had used
  `arena_save()` purely as a heap-cursor probe were rewritten to assert the
  functional result; the no-allocation property is still covered by the
  elide-closure-pair IR tests.

### What was deliberately left alone

The native **two-cursor allocator** (a transient region + a persistent region
for `state`-rooted allocations, toggled by `OpPersistentSet` /
`OpPersistentRestore`) stays. With no `arena_restore`, the transient region is
never bulk-reset, so the two-region split is now largely vestigial — but
collapsing it touches the allocator and the persistent-set IR ops and is a
separate, larger change. Comments that described the regions in terms of
`arena_save` / `arena_restore` were updated to describe RC reclaim.

## Consequence (accepted regression)

RC **cannot collect reference cycles.** The per-request arena reset previously
reclaimed everything a handler allocated regardless of refcount, which made
request-scoped cycles safe. Without it:

> A long-running Fern HTTP server now leaks any reference cycle a handler builds
> per request, accumulating until the process exits — not just cycles reachable
> from `state { }` (which always leaked) but request-local ones too.

For handlers that build no cycles (the common edge-function shape), behavior is
unchanged apart from finer-grained, incremental freeing instead of one O(1)
bulk reset. The cycle-leak exposure is documented in
`docs/CYCLE-COLLECTION-ANALYSIS.md` §4; closing it properly needs a cycle
collector (trial-deletion / backup tracing), which is the right long-term fix
now that the arena safety valve is gone.

## If arena is ever wanted back

Reintroduce `arena_save` / `arena_restore` as runtime helpers (the wasm bodies
were a 2-instruction load/store; the native ones a single ldr/str against
`__fern_heap_ptr`) and re-add the `tcp_serve` / `__http_entry` bracketing. The
`arena { … }` surface syntax, if wanted, is best done as a parse-time desugar to
those calls rather than a dedicated AST node threaded through every pass.
