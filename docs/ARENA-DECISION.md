# Arena: keep-or-remove decision

**Status:** `arena { … }` block syntax REMOVED; `arena_save` / `arena_restore`
builtins KEPT (decided 2026-06-01).
**Owner:** compiler / runtime.

## The question

The language carried an `arena` feature in two layers:

1. **The `arena { … }` block statement** — a reserved keyword (`arena`) and a
   dedicated AST node (`ast.Arena`) that desugared, at IR-lowering time, to
   `arena_save() → body → arena_restore(saved)`. It was a syntactic scope whose
   allocations were meant to be reclaimed at block exit by snapping the
   bump-allocator cursor back.

2. **The `arena_save()` / `arena_restore(handle)` builtins** — leaf runtime
   helpers (`__fern_arena_save` / `__fern_arena_restore` on arm64 / x86-64, and
   the wasm runtime pair) that snapshot and rewind the bump-allocator cursor.

The triggering proposal was to "remove the arena construct" on the premise that
it was unused in production and only kept alive by a handful of dedicated tests.

## What the investigation actually found

That premise was **wrong for layer 2.** The `arena_save` / `arena_restore`
builtins are load-bearing production infrastructure:

- **`internal/stdlib/std/tcp.fern`** — the `tcp_serve` accept loop (the serving
  loop behind *every* HTTP handler, one of the language's two stated use cases)
  brackets each request with `arena_save()` … `arena_restore(saved)`. This is
  the per-request memory reclaim: recv buffers, the parsed `HttpRequest`, the
  handler-built `HttpResponse`, and the serialized wire bytes are all reclaimed
  before the next `tcp_accept`. State-rooted allocations sit below the save
  point (a separate persistent cursor) and survive the rewind.
- **`internal/codegen/wasmbin/wasi_http.go`** — the wasm `__http_entry` wrapper
  uses the same `__fern_arena_save` / `__fern_arena_restore` pair to reset the
  heap at each request boundary, closing the parity gap with the native path.
- Several interaction tests (closure no-alloc probes, the arm64-darwin syscall
  table, both native HTTP-handler e2e tests, `TestBuildHttpHandlerCompiles`,
  `TestWASMArenaReset`) exercise the builtins directly.

Removing the builtins breaks HTTP serving on every backend — confirmed
empirically: a full removal made `TestBuildHttpHandlerCompiles` fail with
`undefined identifier "arena_save"` coming from inside `std/tcp`.

Layer 1, however, **was** genuinely unused: no `.fern` source used the
`arena { … }` block except a stale comment in `examples/wasm/json_pretty.fern`.
The block was sugar over the builtins, and the codepaths that needed per-request
reclaim (`std/tcp`, `wasi_http.go`) call the builtins directly rather than
emitting the block.

## Decision: remove the sugar, keep the engine

- **Removed** the `arena { … }` block statement: the `arena` keyword, the
  `ast.Arena` node and its ~25 statement-walk cases (parser, checker, IR,
  monomorph, closureconv, modload, constfold, treeshake, printer, interp,
  `ast.Walk`), `parser.parseArena`, and the four block-only tests
  (`TestWASMArenaScope`, `TestInterpArenaScope`, and the `Arena` entries in the
  interp coverage and monomorph node-exhaustiveness tables). `arena` is now a
  usable identifier — guarded by `parser.TestArenaIsNotReserved`.
- **Kept** `arena_save` / `arena_restore` (checker `FuncSigs`, interp builtins,
  and the arm64 / x86-64 / wasm runtime helpers) unchanged, since `std/tcp` and
  the HTTP-handler runtimes depend on them.

Rationale: the sugar added a reserved word and a node type threaded through
every AST pass while carrying zero users; the per-request-reclaim capability it
nominally provided is delivered by the builtins, which the serving loop already
calls directly. Dropping the block removes maintenance surface (one fewer
statement node to handle in every new pass) without touching the working
edge-function path.

## If the block is ever wanted back

Reintroduce it as a desugar in the parser/checker layer only — lower
`arena { … }` to `arena_save()` / `arena_restore()` around the block during
parsing or an early desugar pass, rather than carrying a dedicated `ast.Arena`
node all the way to IR lowering. That keeps the convenience without the
per-pass cost, and would need static escape-analysis to be safe for general use
(the old node never enforced "allocations must not escape the block").
