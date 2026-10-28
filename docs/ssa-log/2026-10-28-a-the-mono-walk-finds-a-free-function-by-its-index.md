# 2026-10-28 — the generic walk finds a free function by its index

`parser.free_decl_at` in the generic-struct walk (`ms_call`,
`ms_expr`). Refs #8171.

## The shape

`free_decl_at` found a free function by name with a scan of every
function in the bundle. The generic-struct walk asked it 113 K times
per self-compile, through `me_call_decl_at`, `me_called_name`,
`func_ret_of` and `func_param_type_at`. Each scan cost about 90 K Ir. It
was the most expensive function in the compile: 10.2 G Ir, 7.5% of the
whole.

## What changed

`MFns` already holds `pix`, a name index over the free functions, and
`pat`, the position of each in `decls`. The new method `MFns.free_at`
finds the name in `pix` and returns its position. Each index chain keeps
the order of `decls`, so it finds the same first function that the scan
found. `func_ret_of` and `func_param_type_at` now take the `MFns` and
use `free_at`. `free_decl_at` is deleted.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 8c68804. The earlier subject, main at
ddc5b11, does not compile on main now.

| subject | main at 8c68804 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 136.055 G | 125.267 G (−7.93%) |

The whole-compiler binary is byte-identical to the base compiler's.
