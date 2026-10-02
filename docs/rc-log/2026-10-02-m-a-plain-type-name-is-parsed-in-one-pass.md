# 2026-10-02 — a plain type name is parsed in one pass

`parser.parse_type_ref`, `checker.names_resource_handle`. Refs #8171.

`parse_type_ref` runs about 813,000 times on the stage-2 compile of
`checker.fern`, more than half of them on the checker's 425,000 type
resolutions, and most spellings are plain names (`i32`, `string`, a struct
name).

- **`parse_type_ref`.** A plain name went through every branch of the
  decoder before falling through to the plain case: a full scan for a
  top-level `=>` (`top_arrow_at`), the array, view and tuple checks, and a
  second full scan for a generic's `[`. A spelling with no `[`, `(` or `=`
  can only end there, so `plain_type_spelling` checks for those three bytes
  in one pass and returns the plain `TypeRef` at once.
- **`names_resource_handle`.** Every resolution asked the resource index
  about the name, after `handle_resource` had sliced off an `own ` or
  `borrow ` prefix. A module that declares no resource now answers without
  either.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
is the compiler the self-host compiler builds from the same commit.

| | main (5ac5949) | this change |
|---|--:|--:|
| stage 2, total Ir | 33.64 G | 33.42 G (−0.64%) |
| `parser.top_arrow_at` self Ir | 224.7 M | 77.5 M |
| `parser.parse_type_ref` self Ir | 165.2 M | 84.7 M |
| `parser.plain_type_spelling` self Ir | — | 86.1 M |
| `checker.names_resource_handle` + `handle_resource` self Ir | 46.7 M | 9.1 M |

Byte-identical against a compiler built from main: the `checker.fern` binary
from the same sources, the stage-2 compiler's output, and all 1,965
`selfhost-emit-hashes` rows.

## Leads

- `type_from_spelling` still costs about 1,700 Ir a resolution, and the same
  declared signatures are resolved again: `collect_func_sigs` and
  `build_func_scope` both resolve every parameter and result
  (`param_decl_type`, `func_ret_type`, 130 k calls between them).
- `ssaunits.schema_fields_error` (422 M) and `ssarc.schema_types_error`
  (246 M) re-check every record and enum in a function's schema table for
  each of the 2,408 functions verified. The verdict for an entry depends only
  on the entry and the table, so it could be reached once per module; doing
  so means either the verifier keeps state across functions or the schema
  builder checks its own entries, and the second gives up the verifier's
  independence.
