# 2026-10-02 — three whole-table scans become name lookups

`asmcore.callgate_expr`, `ssarc.caller_sigs`, `semsource.schema_of`. Refs
#8171. No emitted byte changes: the stage0-built compiler before and after
emits the fixed older tree (`examples/self_host/fern.fern` at 1ae9cad,
118,760,465 bytes of x86-64 asm) byte for byte, on main at c987221a and
again at 0d7a8d32; the `selfhost-emit-hashes` sweep is 1,965 rows per
compiler with 0 differing on both bases.

## What the profile named

The whole-compiler emit (`docs/LOCAL-DEV-LOOP.md`, "Where a whole self-host
emit spends its time") after the two scan removals of #10993 still had three
scans in its inclusive top rows:

- `asmcore.check_undefined_calls` 8.12 G of 227.37 G: `callgate_expr` looked
  a callee up by walking the free-function table (`CallGate.names`,
  `string[]`) with a string compare per entry, for every call expression in
  the module, twice (the arity gate walks the same table).
- `ssarc.caller_sigs` 5.21 G, of which `irlower.without_thread_rows_through`
  2.73 G: per produced callee, a scan of every `THREAD<k>:` row to ask
  whether the callee is a threader, and the rows were re-filtered for each.
- `semsource.schemas` 5.74 G, of which `semrecords.find_in` 2.52 G: each
  type reaching `schema_of` was compared with `semtypes.equal` against every
  record already collected.

## What changed

- `CallGate` carries a `util.NameIndex` over `names`; `callgate_expr` asks it
  for the callee's index and reads `argc` at that index. Both gates build
  the index once per module.
- `caller_sigs` builds a `NameIndex` of the threader names once, drops the
  threader rows the first time a produced callee is one, and never again:
  the rows it appends afterwards are not threader rows.
  `without_thread_rows_through` is gone; `thread_row_names` and
  `without_thread_rows` are its two halves.
- `Schemas` keeps a `record_names` index beside `records`, one entry per
  record in order; `schema_of` walks the type's name chain
  (`semrecords.find_named`) and runs `semtypes.equal` only on records of that
  name. `find_in` stays for its other callers.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 (c891ebc) builds from each tree, emitting the fixed older tree
to x86-64 asm text. Both rows are built from main at c987221a and this change
on it.

| | main | this change |
|---|--:|--:|
| total Ir | 227.37 G | 216.02 G (−5.0%) |
| `asmcore.check_undefined_calls`, inclusive | 8.12 G | 0.19 G |
| `ssarc.caller_sigs`, inclusive | 5.21 G | 2.48 G |
| `irlower.without_thread_rows_through` / `without_thread_rows`, inclusive | 2.73 G | 0.12 M |
| `semsource.schemas`, inclusive | 5.74 G | 5.05 G |
| `semrecords.find_in` + `find_named`, inclusive | 2.52 G | 0.71 G |
| `util.NameIndex.added`, inclusive | 0.38 G | 1.20 G |

The last row is the cost the schema index adds: `added` copies the index per
record, so the fixpoint over a function's records pays it once per record.
The lookups it replaces cost more than three times that, and an in-place
append would take the rest.

## Witnessed

`TestSelfHostCheckerCodesX86_64`, `TestSelfHostCheckerCodeSequenceX86_64`,
`TestSelfHostUndefinedCallGate`, `TestSelfHostWasmUndefinedCallGate`,
`TestSelfHostWasmArityGate`, `TestSelfHostGenericArity*`,
`TestSelfHostRetiredArgvBuiltinsUndefined`, `TestSelfHostThreadParam*`,
`TestSelfHostHandbackRebind*`, `TestSelfHostSemanticSource*`,
`TestSelfHostAllocCountMatrixX86_64`, `TestSelfHostIRStrengthPeephole`,
`TestSelfHostFixtureSourcesCheck`, `TestSelfHostFeatureCensus`, the lint
ratchet, the fixed-tree identity and the emit-hash sweep.

## Next

Self cost on this profile: `ssa_lift.lift_impl` 7.05 G, `util.hash_bucket`
6.78 G (#11008 shortens its loop), `__fern_str_eq` 5.20 G, `__fern_alloc`
4.92 G, `__fern_arr_slice` 4.25 G, `irlower.param_is_borrowable` 3.39 G,
`asmcore.infer_call_named_type` 3.10 G, `ir.fold_const_binaries` 2.83 G.
`NameIndex.chain` is 3.56 G inclusive, now mostly `has` and `find` from the
registries' probes rather than from a scan standing in for a lookup.
