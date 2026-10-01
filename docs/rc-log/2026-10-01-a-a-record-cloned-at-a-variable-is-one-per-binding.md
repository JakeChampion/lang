# 2026-10-01 — a record cloned at a variable is one record per binding (#10827)

Self-host (`semsource.resolved`, `semsource.record_sig`,
`semlower.instance_records`, `ircore.with_records`, `ssarc.record_name`).

A generic whose body builds `Slot[T]` (`hold[T](f: () => T)`) is promoted by
the parser's clause (e) and cloned per call site. A call site the
monomorphiser cannot key leaves it erased (`stranded_names`):

1. a function value whose type no declaration spells: `hold(fs[0])`, or
   `keep(f)` in a match arm over `Option[() => string]`;
2. an erased generic forwarding its function parameter,
   `wrap[U](g: () => U) { hold(g) }`, whose call is keyed at its own variable
   (`hold__0_U`).

The erased body builds the front end's `Slot__0_T`, whose field keeps the
variable. The typed lowering refused shape 1 as `hold$i32: record field type`
and shape 2 as `hold__0_U: unresolved parameter type`, so both compiled only
through the AST lowering, and `FERN_SEM_IR_STRICT` failed them.

## The route

Not the monomorphiser. It runs before the checker and spells a function type
as `fn` with sidecars, so typing `fs[0]` or a match binding there means a type
representation the parser does not have. Each extra expression form taught to
`mono_infer` would leave the next one refused. Nor
`promote_erased_forwarders`, which fixes shape 2 only.

The typed lowering already instantiates an erased template per binding
(`hold$i32`). Two changes let those instances build the record:

- Inside a template, `resolved` gives a clone at a variable its variables as
  type arguments (`record_variables`, through nested clones, cut on
  recursion). The binding then substitutes them like any nominal argument, so
  `hold$i32` builds `Slot__0_T[i32]` and a callee template taking one binds
  through it. `record_sig` reads the declaration's fields at those arguments.
  Only a struct named `Base__key` is looked at, since only a clone can hold a
  variable.
- `generic_decl` reads the `fn_ret` / `fn_param_types` sidecars, so
  `hold__0_U`, whose only variable is in `f: () => 0_U`, is a template.

An instance record needs a declaration at emit: the wasm backend stores each
field at the width the declaration names and declares one type-id global per
declared struct. `semlower.instance_records` declares each, named by its type
key (`Slot__0_T$i32`), after the module's struct declarations. ssarc lowers
against the extended table, and the emit entries append the same declarations
after their own normalisation (`gate_program`, the wasm whole-module and
per-module entries, `asm_modload_run`'s verify path), so every index before
them is unchanged.

## Measured

Every leg compiles under `FERN_SEM_IR_STRICT=1`:

| program | x86-64 sanitize | arm64 | wasm32 |
|---|---|---|---|
| `fs[0]` + match arm, i32 / string (production row) | 12 allocs, 12 frees | answers | answers |
| forwarder, i32 / string (production row) | 7 / 7 | answers | answers |
| i64 / f64 bindings, a record holding the record, a forwarder | 18 / 18 | answers | answers |
| `Node[T] { v: T, kids: Node[T][] }` at string and i32 | 11 / 11 | answers | answers |

The AST lowering, for comparison, leaks 200 and 112 bytes on the first two,
168 bytes on the wide one (and emits a wasm module wasmtime rejects:
`type mismatch: expected i64, found i32`), and 416 bytes on the recursive one.

## Still refused

A template bound to a `str` view, with or without a record in it:
`first$str: a view is lent, never retained`. This is not specific to records.
