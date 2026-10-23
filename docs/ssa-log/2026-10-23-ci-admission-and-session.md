# Source admission and session regressions

The full CI run found six regressions beside the lexical source changes.
This change repairs each failure without a new test exception.

The statement checker now accepts an owned array at a local view declaration.
Its diagnostic pass already accepted that conversion.
Both paths now use the same array-lend predicate.

A generic callback first takes the known parameter types of an array method.
Its concrete result then binds the result variable of that method.
This repairs the checked result of `view.transform(identity)`.
The conformance comparison covers both admission paths on every runnable fixture.

REPL lines expose their top-level locals under their source names.
Closure values retain the resolved names inside their capture environments.
The session test covers persistent values, a captured value, a new declaration, and a nested shadow.

The Unicode generator now emits the current names of the public char methods.
The import-free module census records the new Unicode imports of the lexer.
The lexer drivers already copy and qualify imported modules on each target.

The ownership parser test now supplies an owned string to its callback with an `own` parameter.
The lane diagnostic fixture records the trait-bound error that both checkers report.

Wasm composition tests supply stderr for heap abort causes.
Their worlds still prune unrelated interfaces.
Both the embedded world and a user world must produce a valid component and the expected stdout.

Validation passes the full runnable conformance comparison and both checker suites in this change.
The REPL regressions, both Wasm composition tests, and all three lexer targets pass.
The lane error fixture, source checks, Unicode generator tests, Go vet, and formatting also pass.
