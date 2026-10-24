# Generic supertrait arguments

A parent trait carries type arguments through the trait declaration.
For `Child[U]: Parent[Option[U]]`, a `Child[i32]` bound exposes the parent methods with `Option[i32]` arguments and results.
The formatter preserves the same parent spelling.

The Go AST stores parent arguments beside the parent names.
The loader rewrites the argument types through module imports.
The checker substitutes arguments across each parent edge.
It also checks parent argument counts and the arguments of concrete parent implementations.
The comparison normalizes parameters from generic implementations through the existing helper.

The self-hosted parser uses the shared type parser for each parent.
It retains the full spelling in the trait records.
Module flattening resolves the trait head and each argument in their respective namespaces.
The checker then uses those arguments for method lookup and conformance.

The tests cover associated calls, receiver calls, default methods, nested results, generic implementations and import aliases.
Negative cases reject wrong arguments, missing or extra parent arguments, and an incompatible parent implementation.
See the [validation record](../benchmarks/csv-convert-errors-2026-10-24/main-5e637-integration/README.md).
