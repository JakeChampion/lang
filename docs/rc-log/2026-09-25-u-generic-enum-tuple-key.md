# A generic enum at a tuple argument is cloned

`parser.monomorphize_enums` clones a generic enum per instantiation, but a
tuple argument had no clone name. So `Opt[(i32, i32)]` left the whole enum
out of the pass: declaration, variants and methods stayed generic. The typed
path refused every function that touched it ("variant field type"), and the
module fell back to the AST lowering. Three `TestSelfHostSemanticProduction`
rows pinned that fallback with `atLeast: 0`, and under
`FERN_SEM_IR_STRICT=1` each failed.

`ge_targ_mangle` now names a tuple argument the way native's `mangleArg`
does: `tup`, then each element after a `_`, with an array element suffixed
`_arr`. `Opt[(i32, i32)]` is `Opt__tup_i32_i32`. The inferred path, a bare
`Sm((3, 4))`, takes the same name. As for a function-type argument, the key
is only the clone's name. The substitution reads the argument's real
spelling from `iargs`.

The named elements are nominals, arrays of them, and nested tuples of them.
A tuple holding a generic enum (`Opt[(Opt[i32], i32)]`) still has no name,
because the clone's substituted field types would keep the inner enum
unrewritten, so that enum stays out of the pass as before. A multi-parameter
enum keeps refusing any composite argument, since its `__`-joined key would
be ambiguous.

## Tests

- `TestSelfHostGenericEnumTupleKey` runs the parser's passes and checks the
  enum names left: annotated, inferred, nested with an array, beside a simple
  key, and the refused tuple of a generic enum.
- `TestSelfHostSemanticProduction`: the three composite-key rows now produce
  whole (2, 3 and 1 declarations) with no leak. A new row,
  `generic-enum-at-a-nested-tuple-key`, carries an array and a string through
  a method clone for 50 rounds: 500 allocs, 500 frees. Every row runs on
  x86-64 (with the sanitizer too), arm64 and wasm, against the AST lowering.
