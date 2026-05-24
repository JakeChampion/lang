# Test runner migration audit

The pure-Lang test runner (`internal/stdlib/std/test.fern`,
TAP-13) was built so the project's regression suite can
eventually run without a Go dependency — landing point
for the compiler self-host effort. This doc inventories
every `*_test.go` in the repo and classifies each by
**what blocks migration today**.

The runner side is essentially feature-complete: every
assertion shape the Go suite uses has a Lang equivalent,
plus `--filter` / `--fail-fast` / `--quiet` CLI flags,
fuzz harness, bench harness, subsuites + skip + merge,
golden files, file/timing/JSON assertion families,
Option/Result helpers, and so on.
What's left is **which Go tests can flip to Lang now**
vs **which need other work first**.

## Summary

| Category                                  | Files | Status                                                                                            |
| ----------------------------------------- | ----: | ------------------------------------------------------------------------------------------------- |
| **A) Partially migrated (PoC campaign)**  |     1 | `interp_script_test.go` — 5 of its 10 cases now have side-by-side Lang versions; rest unmigratable |
| **B) Subprocess-shape, low migration ROI** |     1 | `check_test.go` — could flip via `subprocess(...)` but stays subprocess-shaped                    |
| **C) Cross-backend orchestration**        |     7 | Inherently spawn N backends per case. Need a Lang-driven multi-backend runner first (see below)   |
| **D) Self-host Lang programs**            |    13 | The `.fern` file IS the test; Go side is a cross-backend gate. Same blocker as C.                 |
| **E) Compiler-internal Go API tests**     |    39 | Call Go-side parser / checker / IR / codegen directly. Gated on **self-hosting the compiler**.    |
| **F) LSP + wasm-binary infrastructure**   |    22 | Test Go service code that has no Lang counterpart (LSP, raw wasm encoding). Likely stay Go.       |
| **G) Test-runner gate itself**            |     1 | `internal/e2e/test_runner_test.go` — collapses to a shell wrapper post-self-host.                 |
|                                           |       |                                                                                                   |
| **Total**                                 |    84 |                                                                                                   |

Roughly: ~1% (case-level: ~5 cases) actively migrated /
in-progress; ~24% (C + D, 20 files) gated on a Lang-driven
multi-backend runner; ~46% (39 files) gated on compiler
self-hosting; ~26% (22 files) likely stays Go forever.

## A) Already migrated

Lang versions live in `examples/tests/*_migrated_test.fern`
with `TestRunner*MigratedExample` gates. Originals stay
live until the wider campaign cuts over.

| Lang file                                  | Original                                                                                                              |
| ------------------------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| `string_prelude_migrated_test.fern`        | `TestInterpScriptStringPrelude` ([#874](https://github.com/JakeChampion/lang/pull/874))                               |
| `unions_migrated_test.fern`                | `TestInterpScriptUnions` ([#878](https://github.com/JakeChampion/lang/pull/878))                                      |
| `header_map_migrated_test.fern`            | `TestInterpScriptHeaderMap` ([#880](https://github.com/JakeChampion/lang/pull/880))                                   |
| `http_request_headers_migrated_test.fern`  | `TestInterpScriptHttpRequestHeaders` ([#882](https://github.com/JakeChampion/lang/pull/882))                          |
| `http_response_headers_migrated_test.fern` | `TestInterpScriptHttpResponseHeaders` ([#886](https://github.com/JakeChampion/lang/pull/886))                         |

## B) Migratable today

What's left in `internal/e2e/interp_script_test.go` after
the PoC campaign:

- `TestInterpScriptFile` / `TestInterpScriptStdin` /
  `TestInterpScriptReadAllStdin` — test the
  `lang -interp` binary's source-loading mechanics
  (file path vs stdin vs piped stdin). **NOT
  migratable**: the Lang version would still need to
  drive the binary as a subprocess to test how it
  consumes its input.
- `TestInterpScriptInteropIntToStringViaMangling` —
  four subcases each in an isolated process to exercise
  different `import` shapes against the modload
  mangling bug. **NOT migratable**: collapsing into one
  Lang file would defeat the per-subcase isolation.
- `TestInterpScriptMissingMain` — tests the error path
  of `lang -interp` itself. **NOT migratable** for the
  same reason as the file/stdin tests.

That essentially **exhausts the easy-migration pool**
inside `interp_script_test.go`. The shape that migrated
cleanly — inline Lang source + check exit/stdout —
appears nowhere else in the repo as of writing.

The `check_test.go` family (6 functions exercising
`lang -check` exit codes + diagnostic text) **technically
could** flip to Lang via `subprocess(...)`, but the
migrated version stays subprocess-shaped — there's no
ergonomic win.

## C) Cross-backend orchestration

Each case compiles a single source through every
available backend (arm64 / x86_64 / wasm) and asserts
they agree on the result. The Lang version of this
shape would still need to invoke multiple compiler
backends from one test driver.

- `internal/e2e/arm64_test.go`
- `internal/e2e/x86_64_test.go`
- `internal/e2e/cross_module_variant_test.go`
- `internal/e2e/float_semantics_test.go`
- `internal/e2e/diff_oracle_test.go` (differential
  oracle: same source, N backends, one expected result)
- `internal/e2e/wasm_e2e_test.go`
- `internal/e2e/wasm_preview2_test.go`

**Unblock:** add a Lang-driven multi-backend runner — a
helper that takes a Lang source string and a list of
backends, invokes `lang` once per backend (or `wasmtime`
for the wasm path), and returns the {exit, stdout}
tuple per backend. Once that lives in `std/test` (or
its own module), every case in this category becomes
`assert_all_backends_match(src, expected_exit)`-shaped.

## D) Self-host Lang programs

The `examples/self_host/*.fern` files are the Go
compiler stages (lexer, parser, checker, IR passes,
codegen) re-implemented in Lang. The Go tests for
those (`self_host_*_test.go`) are **cross-backend
gates** — same `.fern` file compiled by N backends,
expected to exit 0.

- `self_host_lexer_test.go`
- `self_host_parser_test.go`
- `self_host_checker_test.go`
- `self_host_constfold_test.go`
- `self_host_printer_test.go`
- `self_host_disasm_test.go`
- `self_host_interp_test.go`
- `self_host_vm_test.go`
- `self_host_asm_test.go`
- `self_host_asm_run_test.go`
- `self_host_arm64_emit_test.go`
- `self_host_cross_validation_test.go`
- `self_host_pipeline_test.go`

The `.fern` files **already are the tests** — they
contain in-program assertions and return non-zero on
failure. The Go gates only multiplex them across
backends.

**Unblock:** same as C — once a Lang-driven multi-
backend runner exists, these become trivial Lang
`r.it("lexer (arm64)", run_backend("arm64", lexer_src))`
calls.

## E) Compiler-internal Go API tests

Build Go AST values, call Go checker functions, inspect
Go IR opcodes, etc. The Go tests poke at internals
that aren't reachable from Lang because the compiler is
in Go.

Parser / checker / type system:

- `internal/parser/parser_test.go`
- `internal/parser/fuzz_test.go`
- `internal/checker/checker_test.go`
- `internal/checker/fuzz_test.go`
- `internal/lexer/lexer_test.go`
- `internal/ast/walk_test.go`

IR passes (each test directly constructs IR functions
and runs a pass on them):

- `internal/ir/ir_test.go`
- `internal/ir/constprop_test.go`
- `internal/ir/copyprop_test.go`
- `internal/ir/dce_test.go`
- `internal/ir/defunctionalise_test.go`
- `internal/ir/elide_test.go`
- `internal/ir/flatten_test.go`
- `internal/ir/fold_test.go`
- `internal/ir/inline_test.go`
- `internal/ir/inline_zero_capture_test.go`
- `internal/ir/strength_test.go`
- `internal/ir/tco_test.go`
- `internal/ir/tee_test.go`

Other Go-side passes / helpers:

- `internal/constfold/constfold_test.go`
- `internal/closureconv/closureconv_test.go`
- `internal/treeshake/treeshake_test.go`
- `internal/shadowrename/shadowrename_test.go`
- `internal/monomorph/monomorph_test.go`
- `internal/modload/modload_test.go`
- `internal/diag/diag_test.go`

Interp + codegen + printers:

- `internal/interp/interp_test.go`
- `internal/interp/coverage_test.go`
- `internal/printer/diff_test.go`
- `internal/printer/format_test.go`
- `internal/printer/roundtrip_test.go`
- `internal/codegen/wasm/wasm_test.go`
- `internal/codegen/wasmbin/wasmbin_test.go`
- `internal/codegen/wasmbin/build_test.go`

Stdlib + utility:

- `internal/langsmith/langsmith_test.go`
- `internal/langsmith/fuzz_test.go`
- `internal/langsmith/gtype_internal_test.go`
- `internal/langstring/langstring_test.go`
- `internal/stdlib/stdlib_test.go`

**Unblock:** **the compiler being self-hosted.** Once
the parser / checker / IR / codegen are themselves
written in Lang and exposed as `std/compiler/...`
modules, these tests can call those modules from Lang
and assert on the results. Tracked separately (the
other Claude session's mandate).

Until then, these stay Go. There is no useful interim
migration shape — wrapping a subprocess wouldn't
preserve the introspection these tests rely on.

## F) LSP + wasm-binary infrastructure

Service-level tests for code that has no Lang
counterpart and probably never will:

LSP server (Go-only — the LSP runs as a Go binary
talking JSON-RPC):

- `internal/lsp/cache_test.go`
- `internal/lsp/completion_test.go`
- `internal/lsp/definition_test.go`
- `internal/lsp/formatting_test.go`
- `internal/lsp/hover_test.go`
- `internal/lsp/inlay_test.go`
- `internal/lsp/references_test.go`
- `internal/lsp/semantic_tokens_test.go`
- `internal/lsp/server_test.go`
- `internal/lsp/signature_test.go`
- `internal/lsp/symbols_test.go`
- `internal/lsp/workspace_test.go`

Wasm binary encoding (Go-side byte-level emitter — the
compiler invokes this from Go, not from Lang):

- `internal/wasm/componenttype/componenttype_test.go`
- `internal/wasm/convert/convert_test.go`
- `internal/wasm/encode/encode_test.go`
- `internal/wasm/imports/imports_test.go`
- `internal/wasm/inst/inst_test.go`
- `internal/wasm/leb128/leb128_test.go`
- `internal/wasm/memory/memory_test.go`
- `internal/wasm/module/module_test.go`
- `internal/wasm/numeric/numeric_test.go`
- `internal/wasm/sections/sections_test.go`

**Unblock:** none planned. These stay Go even after
self-hosting unless the LSP / wasm-emitter themselves
get rewritten in Lang, which isn't a stated goal.

## G) Test-runner gate itself

`internal/e2e/test_runner_test.go` — the file that
runs every `examples/tests/*.fern` through `lang
-interp` and pins TAP outputs. **Collapses to a shell
wrapper post-self-host** (`lang test_dir/*.fern` would
just be the test command).

Right now it's the most useful Go test in the repo
for the migration effort — every migrated example
gets a regression gate here.

## Suggested migration order

Once the unblockers land, work the categories in this
order — biggest test-count payoffs first, easiest to
hardest:

1. **D) Self-host programs** (12 files). All
   structurally identical — same `compileAndRunBackend`
   pattern. A Lang-driven multi-backend runner unlocks
   the whole set in one stroke; each migrated file is
   a 3–5-line `r.it("stage (backend)", ...)` per
   backend.
2. **C) Cross-backend orchestration** (7 files). Same
   shape as D, less repetition per file but still
   gated on the same multi-backend runner.
3. **E) Compiler-internal** (33 files). The big one.
   Each `internal/<pass>/...` test moves once the pass
   ships in `std/compiler/<pass>`. Order: lexer → parser
   → checker → IR-passes → codegen, mirroring the
   self-host stage order in
   `examples/self_host/`.
4. **B) Remaining `interp_script_test.go`**. ~3 cases
   plus `check_test.go` — pure ergonomic improvements
   (subprocess shape stays). Pick up alongside the
   broader campaign.

## What the runner gives you today

For reference, the Lang assertion surface that's
already in place — every entry has a Go-suite
analogue and the migration playbook in
`examples/tests/string_prelude_migrated_test.fern`
(et al) shows how the shapes line up:

- Numeric: i32 / i64 / u32 / u64 / f32 / f64 eq/neq/
  relational/range/near/rel/exact/NaN
- Arrays: deep eq across all widths, set / subset /
  intersects / disjoint, prefix / suffix / subseq,
  sorted (asc/desc/strict), unique, contains, count,
  all / any predicate, one_of / none_of, position-spot
- Strings: eq/neq/diff, contains/starts/ends (+ case-
  insensitive + multi-option), all-substring,
  multi-substring, count
- Maps: len / has / lacks / deep_eq
- Options + Results: is_some / is_none / is_ok / is_err
  (+ value-compare variants)
- Process / file / env / JSON (with deep-eq and field-
  extraction) / golden files / timing / bench / fuzz
- Subsuites + skip / skip_if / merge / log / log_kv /
  defer_cleanup
- CLI: `--filter` / `--fail-fast` / `--quiet`

See `docs/STDLIB.md` for the canonical reference.
