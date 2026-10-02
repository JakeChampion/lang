# 2026-10-02 — the checker reads a spelling once per module

`checker.type_from_spelling`, `with_spellings`, `module_spellings`,
`TypeNames`, `StructTable`, `FuncSig.typevars`, `collect_func_sigs`,
`settle_shared_literal_typevars`; `asmcore.field_type_tag_in`. Refs
#8171, #11066. No emitted byte changes:
the `selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0
differing against a compiler built from main at cede3aaf. (The
`checker.fern` binaries differ, as they must: this change is in
`checker.fern`.)

## What the profile named

`type_from_spelling` was 766 M of the 33.63 G stage-2 compile of
`checker.fern`: 424,753 calls, each parsing its spelling
(`parse_type_ref`, 564 M) and walking the resolution ladder. An
`eprint` on the spelling, over the same compile, counted 432,171 calls
over 374 distinct spellings; the twenty most frequent (`string`, `i32`,
`ast__Expr`, `boolean`, `string[]`, …) were 348 k of them. A spelling
resolves to the same type wherever the module writes it, since the
ladder reads only the declared names.

`settle_shared_literal_typevars` was 218 M more: for every call of a
declared function it asked `gc_is_typevar_name` of each parameter
spelling, a parse each, and `gcall_tvars` asked the same question of
the same signature again at the binding.

## What changed

- `SpellMemo` is the module's spellings, each resolved once against its
  declared names; `TypeNames` carries it, `StructTable` keeps it so
  `Scope.type_names` hands it on, and `type_from_spelling` reads a
  spelling it holds instead of parsing. `with_spellings` fills it at
  the three places a module's tables are built, from
  `module_spellings`: parameters, returns, fields and the `var`
  annotations on the statement spine (`astwalk.fold_stmt_spine`). A
  spelling the walk does not reach, a lambda's parameters or a `zero`,
  is parsed where it is read, as every spelling was.
- `FuncSig.typevars` is the parameter spellings that are type variables
  of the callee, settled in `collect_func_sigs` against the same
  `TypeNames` a scope resolves with; the literal settling and the call
  binding read it, and `gcall_tvars` is gone.

The memo and the signature field are read-only after the tables are
built, so no scope threads them: a scope's `TypeNames` is built from
the same struct, union and resource names the memo was resolved
against, which is what makes a hit the parse's answer.

A first cut walked every expression too (`astwalk.fold_stmt_nodes`,
for lambda parameters and `zero` types) and spent 355 M in the walk,
most of what the memo saved; the statement spine is 67 M.

## The gate bug it hit

The memo's read, `tn.spellings.names.find(name)`, compiled in
`fern.fern` and was refused in the checker-codes driver with
`E003: variable 'at' declared i32 but initializer has type Option[i32]`,
from the pre-codegen gate (`asmcore.infer_expr_type`), not the checker:
the checker typed the call `i32` in both programs. The gate's
`field_type_tag_in` returned `ty_from_name` of a field's spelling, which
drops a declared struct name to unknown, so `tn.spellings` was unknown
and `.names` fell to the by-name scan over every struct, which in that
program answered with a `names: string` field, and `.find` typed as the
string method. `field_type_tag_in` now keeps a declared struct name
(`struct_local_tag`), as a local of that type already did, so a read
through a struct-typed field resolves within that struct.
`TestSelfHostGateFieldThroughStructField` pins it with the shape; it
fails on main. The by-name scan itself stays for an object the gate
cannot type, which #11066 records.

The stage0 pin compiles the self-host sources with the gate as it was,
and the driver builds (`asm_run.fern`, `wasm_ir_run.fern`) go through
the pin, so `type_from_spelling` reads the memo through a local
(`var memo: SpellMemo = tn.spellings`), the shape the old gate types
right; a pin carrying the fix lets it read the field chain directly.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at cede3aaf and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 33.63 G | 33.18 G (−1.33%) |
| stage 2, `type_from_spelling` inclusive Ir | 766 M | 169 M |
| stage 2, `parser.parse_type_ref` inclusive Ir | 564 M | 310 M |
| stage 2, `with_spellings` inclusive Ir | 0 | 150 M |
| stage 2, `settle_shared_literal_typevars` inclusive Ir | 218 M | 23 M |
| stage 2, `collect_func_sigs` inclusive Ir | 194 M | 123 M |

## Witnessed

`TestSelfHostChecker*` (the differential suites compare the checker
against the native one over the corpora), `TestSelfHostGeneric*`,
`TestSelfHostSemanticSourceRC`, the lint ratchet, `make fmt-check`, and
the emit-hash sweep.

## Next

`parse_type_ref` keeps 310 M: `arg_bindings` 19 k calls, `settle_to_name`
86 k, `spelling_is_view` 34 k, and the generic path
(`type_from_ref_subst`) parses per call too. `gc_is_typevar_name` is
166 M from `gc_bind_param`. `sig_bucket_from` hashes a byte at a time,
233 M over 872 k lookups, and a four-byte step measured no gain: the
bounds check per byte, not the roll, is the cost.
