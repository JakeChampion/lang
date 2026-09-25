# The process, clock and random leaves take the typed path

2026-09-25. Self-host typed path. Step 1 of retiring the AST lowering
(`SELFHOST-SEMANTIC-SOURCE.md`, "Retiring the AST lowering"), continued from
`2026-09-25-c-stdio-helpers-take-the-typed-path.md`.

## What changed

Ten more helpers are retyped the way the stdio writers were: a
`__raw_scratch` / `__raw_alloc` / `__raw_arr_box` block is a `usize`, a
syscall word an `i64`, and an `i64` result is narrowed with `as i32`.
Seven of them are emitted one per source and now take the typed path:
`sleep_ms`, `sleep_ns` (both targets' variants, including Darwin's
`__syscall5` select), `random_i32`, `random_bytes`, `cpu_count`, `isatty`,
`process_alive`.

`umask`, `priority` and `sync` check too, but they are appended to the
filesystem bundle: one source holding every fs helper. A bundle takes the
typed path only when all of it checks, so these three wait for the fs
helpers.

`process_alive` had no semsource contract, so any module calling it kept
the AST lowering. It has one now, and an ssarc arm onto
`op_process_alive`.

## Measured

- `TestSelfHostSemanticProduction` row `process-helpers-take-the-typed-path`:
  a module calling all ten builtins produces whole, `produced` for the
  seven helpers on x86-64 and arm64, the same result as the AST leg (and as
  native), and a clean sanitize leg. Native-only: wasm refuses several of
  these builtins by name.
- Compiling the same program for arm64-darwin reports both sleep helpers
  produced, so the `__syscall5` variants check as well.
- Helper sources that check on their own: 41 of 128, up from 33.
