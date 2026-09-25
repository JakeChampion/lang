# An owned dyn value over a generic implementation

A dyn value is released by a drop that tests the shape its box carries at
offset 0 against every concrete the dyn type can hold (`ssarc.drop_dyn`,
over `ssasem.Func.dyns`). `semsource.dyn_concretes` listed each implementor
by its declared name. A generic one, `impl[T] Shape for W[T]`, had no
concrete by that name, so its type was marked open (`open_dyns`). Any unit
whose release reached it was refused ("a dyn value over a generic
implementation is lent, never owned"), and the AST lowering stood.

Monomorphisation already enumerates the instances. The struct pass clones
`W[string]` as `W__string` and drops the generic declaration; the enum pass
does the same for an enum it can key (`Opt[i32]` becomes `Opt__i32`). A dyn
method call already dispatched to the clones' methods. `dyn_concretes` now
lists a generic implementor's clones, records and enums named `W__…`, as
concretes of their own, so the drop releases each through its own helper.

The open marking goes, and so do `generic_implementor`, `open_dyn_error`
and `reaches_open_dyn`. A generic implementation the passes leave generic,
an enum they cannot key, never reaches that check: the schema of its erased
declaration refuses every function whose drop table reaches it ("unresolved
variant field type").

The first cut listed struct clones only. A dyn value over `Opt__i32` then
released its box without its string payload: 40 allocations, 30 frees. The
old refusal had been covering that case too.

## Tests

`TestSelfHostSemanticProduction`, all with no leak, on x86-64 (with the
sanitizer too), arm64 and wasm:

- `an-owned-dyn-value-over-a-generic-implementation`, the row that pinned
  the refusal: 58 of 58 produced, 42 allocations and 42 frees.
- `a-dyn-array-over-a-generic-implementations-instances`: `W[string]`,
  `W[i32]` and `W[W[string]]` beside a plain implementation in one dyn
  array, 30 rounds: 340 and 340, answering 44.
- `an-owned-dyn-value-over-a-generic-enum-implementation`: 40 and 40.
