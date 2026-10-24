# Struct patterns in enum payloads

A payload slot can contain a struct pattern at any nested variant depth.
The coverage check now resolves that pattern against the declared payload type.
It orders fields by the declaration and checks nested field patterns at their concrete types.
Boolean field patterns can cover both values, including in a generic struct.

The parser marks generated probe matches with their source origin.
Their fallback arms do not produce unreachable-arm errors.
Written matches and invalid primitive patterns retain their diagnostic checks.
The module rewrite also preserves imported names in nested source patterns.

Both front ends preserve a whole-value name in a nested struct pattern.
The Go checker binds that name and checks opaque field access.
Both generic-type rewrites retain the concrete struct name in a nested pattern or destructure marker.
The marker rewrite preserves the field list and requires the initializer to name the same generic family.

The focused suite passes in 25.756 seconds.
Both checkers, both interpreters, and x86-64, ARM64, and Wasm pass.
The execution tests require strict semantic lowering, sanitizers, and a balanced allocation census.
Seven invalid cases retain their required diagnostics in both checkers.
The complexity gate passes without a baseline change.

The Go monomorphization and complete checker suites pass in 3.498 and 7.650 seconds.
The Go parser suite also passes.
The formatter parity case for the nested whole-value pattern passes in 1.635 seconds.
