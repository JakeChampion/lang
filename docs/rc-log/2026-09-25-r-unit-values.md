# The unit value takes the typed path

Under `FERN_SEM_IR_STRICT`, `TestSelfHostUnitTypeX86_64` was refused. Its
program uses `()` everywhere #8759 gave it a type: a parameter, a binding
with and without an annotation, a tuple element and a variant payload. Only
the payload lowered. A void variant payload is a variant with no fields, so
`Ok(())` never needed a value. Every other position needs one.

The unit value is the constant 0 in an i32-shaped slot, which is how the AST
lowering already has it, so a unit argument crosses a call the same way on
both. Void was admitted only as a function result (`concrete(t, true)`). It
is now admitted in the positions that hold a value:

- a parameter, in the body and in its contract;
- a binding, which `declare` had replaced with the closure-slot fallback, so
  the checker's void read as "not yet checked";
- a tuple element (`semtypes.concrete_list`, which a function type's
  parameter list shares), its projection, and a name destructured from it
  (`bind_projection`);
- a call's parameter in the verifier;
- a function value's parameter: `ssasem.func_shape_error` already took a void
  result as a word, and a unit parameter is one too. Before, naming
  `sink(u: ())` as a value refused it ("function signature slot"), and every
  declaration calling it went with it.

The literal `()` defines constant kind 1 at type void, and ssasem's verifier
accepts it with a zero immediate and no text. A void CALL in value position
is still refused ("unsupported void call"), since nothing is pushed for it.

## Tests

- `TestSelfHostUnitTypeX86_64` compiles under `FERN_SEM_IR_STRICT=1`.
- `TestSelfHostSemanticProduction` row `the-unit-value-in-every-position`
  covers a parameter, both bindings, a unit read back out of each end of a
  tuple and destructured from it (a name and a discard), a payload, a
  function value with a unit parameter and a closure capturing a unit: 5 of
  5 produced, with no leak, on x86-64 (with the sanitizer too), arm64 and
  wasm. Before, 0 of 5.
