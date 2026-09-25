# A runtime helper's typed lowering may define drop helpers

2026-09-25. Self-host typed path. Step 1 of retiring the AST lowering,
continued from `2026-09-25-d-process-helpers-take-the-typed-path.md`.

## What changed

`semlower.runtime_bodies` refused any helper source whose typed lowering
defined a drop helper, and it checked a source without the builtin enums a
program's module carries. The filesystem bundle opens with `io_error`, which
builds an `IoError`, so no bundle could take the typed path. Both limits are
lifted:

- The source is checked with `parser.inject_builtin_enums`, the variant
  structs `module_with_builtins` gives a program. Before, `NotFound(path)`
  had no semantic contract.
- The drop helpers each body defines follow the bodies in the result,
  deduplicated and conflict-checked by `ssarc.merge_helpers`, the same merge
  the program's substitution uses. Any conflicting helper refuses the source.
- Both backends emit bodies past the declarations through one
  `emit_named_bodies`. It records each symbol on `EmitState.named` and skips
  one the file already defines, so the program's `IoError` drop helper and
  the bundle's are one definition. arm64's inline loop is gone in favour of
  the shared function.

`io_error` and `path_copy` are retyped (`usize` pointers), so a program
calling only `sync`, `umask` and `priority` now takes the typed path for its
whole bundle.

## Measured

- `TestSelfHostSemanticProduction` row `fs-bundle-takes-the-typed-path`: a
  program that builds and matches an `IoError` itself and calls the three
  fs leaves. Both the module and the bundle produce, the answer matches the
  AST leg on x86-64 and arm64, and the sanitize leg reclaims everything. The
  same program built for both targets assembles with one definition of the
  shared drop helper.
- Helper sources that check on their own: 42 of 128.
