# A dyn downcast on the typed path

The typed path refused `x as? T` on a dyn value (#10323). The AST lowering
emitted `op_dyn_downcast`, which reads the box's shape and allocates the
option in one step. The typed path now lowers it as a test and a branch:

- `ssasem.dyn_is` tests the box's shape against `T`. ssarc emits it as the
  member test, `op_variant_is(T)`: a dyn box carries its concrete's shape at
  offset 0 exactly where a member box carries its variant's.
- `ssasem.dyn_as` is the same box typed as `T`, an identity projection like a
  variant narrowing, and valid only where a `dyn_is` has settled it.
- The branch joins `Some(narrowed)` and `None`. `Some` holds a unit of the
  box, supplied the way any projection's is.

Two more refusals in the same test file were separate gaps:

- A dyn call's arms were every method of the right name, on any type. A
  type with an unrelated method of that name, outside the dyn, made the arms
  disagree. `dyn_arms` now keeps only receivers that implement the whole
  trait set, from the module's impl table.
- A float literal beside an `f32` operand was produced as `f64`, so
  `d.v() + 0.5` refused as `f32 + f64`. It now takes the other operand's
  width, as an integer literal already did.

`TestSelfHostDynTraitIR` moves to the CLI with this change, and every case
now requires a balanced leak census on x86-64 and wasm.
