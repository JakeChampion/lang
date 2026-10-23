# Unit payloads in generic functions

A generic type variable can bind to void. The unit value is concrete, while
unknown types and template variables remain invalid bindings.

A matched unit payload now binds the unit constant. It needs no stored field.
A constructor evaluates that unit value and omits its physical payload. This
lets `Result[void, E].ok()` and `map_err()` rebuild their success variants.

Both checkers accept a unit binding as a constructor payload. They continue
to reject a direct void call there with E072. A generic unit result can also
initialize a local binding.

The conformance case covers both Result variants, `is_ok`, `is_err`, `err`,
`ok`, `map_err`, Option callbacks, and a generic identity at void. It runs in
both interpreters and on x86-64, ARM64, and Wasm.

This resolves #12845.
