# A dyn value is counted

2026-09-23. On the typed path a dyn value used to be lent and never owned
(`2026-09-23-dyn-trait-dispatch-on-the-typed-path.md`). So the typed path
refused any module that:

- rebinds a dyn local (`a dyn value is lent, never owned`)
- returns one (`… never retained`)
- stores one in a field, element, payload or cell (`dyn value is not a field`
  / `… an element`)

All of these now produce, and each releases what it built. So does a
scalar or string coerced to `dyn`, which was refused outright
(`… declared dyn Show holds a semantic value of i32`).

## The model

A dyn value is still the struct or enum box it was widened from, and
`ssasem.dyn_up` is still a projection. What changed is who may hold a unit of
it. Any holder may now: a phi, a call result, a counted parameter, a record
field, an array element, an `Option` payload, a map value, a closure capture.
Retaining one is `rc_inc` on the box.

Releasing one tests the box's shape at run time:

- **`ssasem.Func.dyns`** has one `(dyn type, concrete)` row for every record
  and enum that implements the whole trait set. `semsource.env_rows_closed`
  closes the rows over the schema table, the way it closes closure
  environments, so a concrete's own fields get schemas too.
  - The set belongs to the type, read off the module's impl table. It is not
    what the frame saw built, because a frame that only receives a dyn value
    still has to release it.
- **`ssarc.drop_dyn`** runs under the uniqueness test `drop_value` already
  emits:
  - For each record concrete with children, it emits `op_variant_is(name)`,
    then that record's `__sem_drop_<T>`.
  - An enum's helper tests its own variants, so it is called unguarded.
  - A concrete without children needs no arm, because the box release frees
    it.

## A primitive is boxed

A scalar or string has no shape at offset 0, so widening one is a
construction, not a projection. **`ssasem.dyn_box`** (kind -47) takes a
unit of its operand and lowers to `op_dyn_box`, the op the AST lowering
already emits. That op builds a cell `[shape, value]`, where the shape is
the primitive's name. The cell is laid out like a one-field record on
every backend (the value at byte 8), so `op_struct_get(0, 0)` reads it
back.

- A string implementation is a concrete in `Func.dyns` like a record. Its
  arm in `drop_dyn` tests `op_variant_is("string")` and releases the
  string it reads out of the cell.
- A scalar's cell has no children, so it gets no arm, and the box release
  frees it.

## What is still refused

- **An `i64` or `f64` coerced to `dyn`** (#10098). `ssasem.boxes_into_dyn`
  admits only one-word values, because the wasm box stores and unboxes an
  i32. The AST lowering refuses these too, so a module holding one does not
  build on the self-host at all. Native builds it.
- **Owning a dyn value whose type a generic declaration implements.**
  `impl[T] Shape for W[T]` has one concrete per instance. The release would
  have to enumerate those instances module-wide, and a frame cannot see them.
  - `Func.open_dyns` names such types.
  - `ssaunits.plan` refuses any owned value whose release reaches one: "a dyn
    value over a generic implementation is lent, never owned".
  - A frame that only borrows one still produces
    (`a-borrowed-dyn-value-over-a-generic-implementation`).
  - Before the refusal, a returned `W[string]` leaked its two strings on every
    trip (384 B over six).

## Measured

x86-64, sanitizer build, six trips per shape. Answers match native, and the
typed path produces every declaration.

| shape | AST lowering | typed |
|---|---|---|
| rebound at a phi | 18 of 36 freed, 720 B live | 36 / 36, 0 |
| returned | 12 of 24, 480 B | 24 / 24, 0 |
| record field | 18 of 30, 480 B | 30 / 30, 0 |
| array of two | 25 of 54, 1160 B | 54 / 54, 0 |
| parameter handed back | 12 of 24, 480 B | 24 / 24, 0 |
| `Option` payload | 18 of 30, 480 B | 30 / 30, 0 |
| map value, three superseded | 20 of 32, 480 B | 32 / 32, 0 |
| captured by a closure | 12 of 24, 480 B | 24 / 24, 0 |
| i32 / string / record widened, returned and bound | 9 of 24, 584 B | 24 / 24, 0 |

The mixed module holds in both directions (the `skip` legs of the returned
and handed-back rows):

- The dyn value a produced frame receives from an AST-lowered producer is not
  released twice.
- An AST-lowered caller of a produced pass-through reads its declared modes.

Both leak exactly what the AST lowering alone leaks.

A third direction needed a contract change. An AST frame that builds a
`dyn T[]` literal frees its element boxes at exit, and only when every callee
it lends the array to promises to keep no reference to it. That promise is
the callee's bare `borrowable_params` row. `ssarc.caller_sigs` cleared the
bare row for every produced callee and advertised its borrows only as `CNT:`
rows, which say the callee may retain. Once a `dyn T[]` parameter produced,
the AST caller lost its element sweep and leaked the boxes that the all-AST
module freed. `bare_flags` now gives the bare row back for a borrowed
parameter the callee keeps nothing of:

- no plan supply retains it or anything projected from it
- nothing projected from it is handed to a call, except a dyn dispatch, which
  lends its operands

`a-dyn-array-lent-to-a-produced-callee` is the row.

## Traps

- **The native compiler is the oracle for these answers, and it segfaults on
  the handed-back row until #10072 lands.** The row compares the two self-host
  lowerings, so it does not depend on that fix.
- **Two self-host checker gaps turned up while writing the rows.** The
  checker accepts a `Map` without `import "core/map"` (#10094) and the
  removed in-place `m.set` (#10095). On the second, the two lowerings then
  disagree: the typed path answers 0 and the AST answers 35. The rows use
  `insert` and the import, which native accepts.
- **A heterogeneous literal into a `dyn Show[]` fails the self-host checker**
  (E034, #10097). The array rows build their elements through a function
  that returns `dyn Show`.
- **The AST lowering aborted (134) when a string was written straight into
  a `dyn` field** (`Holder { d: "h" + … }`). It boxed at a dyn var,
  assignment, return and argument, but not at a struct literal field, so
  its dispatch found no arm. The struct literal now lowers a dyn field
  through `lower_dyn_arg` like the other sites, and
  `a-boxed-string-dyn-held-in-an-array` holds the two lowerings to one
  answer.
- **A capture row must not rebind the captured dyn.** Native refuses that
  with the enclosing-scope half of E049, and the self-host does not have that
  half (#8440).
