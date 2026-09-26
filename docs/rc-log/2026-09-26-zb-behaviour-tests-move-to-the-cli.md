# Behaviour tests move to the CLI

Step 3 deletes the AST lowering, and every stdin driver compiles through it
(`2026-09-26` inventory in `SELFHOST-SEMANTIC-SOURCE.md`). A test that checks
what the language does has to move to the self-host CLI first, which lowers
through the typed path and, under the package's `TestMain`, strictly.

`selfHostCLI.exitOf` is the helper: it writes a program to a file, compiles it
for x86-64, arm64 or wasm, runs it, and returns stderr and the exit code. This
change moves 50 test files onto it. Each had an x86-64 test behind an
`asm_pathprobe_run` "ir" assertion and a wasm test over the same case table;
each now has one test over both targets, some over arm64 as well. The path
probe has nothing to assert under a strict compile, so it is gone from them.

Moving them surfaced two kinds of problem.

- **Programs that were not valid Fern.** The stdin drivers resolve no imports,
  so they accepted `.to_string()` on an i32 without `std/i32`, Map operations
  without `core/map`, and a `str` slice assigned to a `string` field. Nine
  files now import what they use.
- **Typed-path bugs.** A `?` failure exit skipped the pending defers (#10322),
  and five shapes refuse (#10323). Seven files stay on the drivers until each
  is fixed; the fix moves its file.

## Measured

- 50 files, x86-64 and wasm (arm64 on two): all pass strict.
- Compile cost: about 45 s once per test process to build the CLI, then 20 ms
  per case, or about 0.5 s for a case that imports the stdlib.
