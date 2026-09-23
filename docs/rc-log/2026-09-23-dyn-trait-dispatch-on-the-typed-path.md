# `dyn Trait` dispatch on the typed path

2026-09-23. A module with a `dyn Trait` value kept the AST lowering. It
refused at the first method call on a dyn receiver (`unsupported call target:
dyn Shape.area`) or at the coercion (`binding … declared dyn Shape holds a
semantic value of Square`). `conformance/cases/dyn_trait_dispatch` was the last
corpus case held by something other than a view escape. It now produces
6 of 6.

## The model

A dyn value in the self-host is the struct or enum box it was widened from:
the dispatch reads the shape at offset 0. The typed path keeps that
representation and treats a dyn value as a borrow of the box.

- **`ssasem.dyn_up`** (kind -46) widens a record or enum value to a dyn type.
  It is a projection, like `variant_up`. The box stays the unit of whoever
  owns it, and the dyn value is anchored to it, so the box outlives every use
  of the dyn value. `ssarc` lowers it as an identity op.
- **A dyn value is lent, never owned.** A unit of one could only be released
  by dispatching on the shape to find the children. `__fern_rc_dec` frees the
  box and nothing under it. So `ssaunits.plan` refuses a dyn value that the
  frame would own ("a dyn value is lent, never owned"): a counted parameter,
  a call result, a phi of two widenings. It also refuses one it would retain
  (a returned dyn, "a dyn value is lent, never retained"). `ssasem` placement
  refuses a dyn element, payload, cell slot or field ("dyn value is not a
  field" / "… an element").
- **A method call on a dyn receiver** is a `call` whose contract is named
  `dyn <method>|<traits>`. The tail is the tag `ir.op_dyn_dispatch` carries,
  and no declaration can spell that name. `semsource.dyn_arms` lists the
  implementations with the backends' own search (`parser.dyn_arm_matches`,
  less array receivers). The contract is their shared signature with the dyn
  type at the receiver. They must agree on every other operand and on the
  result, and they must lend every operand; anything else is refused and
  names the implementation. The operands are copied into a run of
  consecutive values. That run is the slot range the dispatch reads, and
  wasm types those slots by value, so a float operand lands in an f64 local.
  `ssarc.dyn_site` emits the dispatch there and checks that the run is
  consecutive.

Both directions of the mixed module hold:

- A produced dispatch reaching an AST-lowered implementation is a callee the
  AST lowering defines, and `semlower.escaping_callee` now reads every arm of
  an `op_dyn_dispatch`.
- The implementations a produced dispatch reaches keep the modes they
  declared. `Built.dyn_arms` pins them out of the ownership inference, and
  `ownership.call_carried` reads a dyn call as lending.

## Measured

x86-64, sanitizer build, 100 trips of a dyn value over a record that owns a
string (`dyn-over-a-counted-record`):

| lowering | allocs | frees | live |
|---|---|---|---|
| AST | 400 | 201 | 7952 B in 199 blocks |
| typed | balanced | | 0 |

Corpus census: every case but the two view escapes
(`alloc_flat_method_identity_return`, `string_slice_option`) produces whole.

Production rows (`TestSelfHostSemanticProduction`), all four legs:

- with an absolute leak pin:
  - `dyn-dispatch`
  - `dyn-over-a-counted-record`
  - `dyn-method-operands-and-results`: string, i32 and f64 operands; string
    and f64 results; record and enum implementations; an enum temporary
    widened at the argument
- mixed-module legs:
  - `dyn-dispatch-into-ast-implementations`
  - `ast-dispatch-into-produced-implementations`
- one refusal row each for a rebound, a returned and a field dyn value

## Found on the way

- **The AST lowering failed validation on wasm** for a dyn method with a
  float argument. `lower_call_dyn_method` stored the f64 into an untyped
  (i32) local. The argument slots are now typed by width (#10056). The
  `dyn-method-operands-and-results` wasm leg is its gate.
- **Native crashed on x86-64 and arm64** when a fresh enum temporary was
  coerced to dyn at a call argument. `stashOwnedArgTemp` typed the stash by
  the enum, so the post-call release ran the enum's drop on the
  header-less `{data, vtable}` cell (#10053). The stash is now the coerced
  `DynTraitType`, and it is not stashed where dyn is not reclaimed.
  `TestDynCoercedArgTempReleasedAsDyn` crashed on both natives without the fix;
  `TestDynCoercedArgTempBounded` grew on x86-64 and wasm.
- Filed, next:
  - #10054: the native cell for a non-fresh coerced argument is never freed,
    16 B per call.
  - #10055: the self-host checker accepts a non-implementing value into a dyn
    slot, which native refuses with E003/E038.

## What it does not reach

- A primitive or string widened to dyn needs `op_dyn_box`, which allocates a
  box the frame would own, and is refused.
- A generic record, a dyn-to-dyn widening between trait sets, and a dyn value
  anywhere it would be owned are also refused.
