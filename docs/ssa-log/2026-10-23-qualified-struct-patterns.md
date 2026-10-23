# Qualified struct patterns

The Go parser now accepts qualified struct names at all three irrefutable bind sites.
These sites are `let`, parameters, and `for` headers.
The parser retains the qualifier until the module loader resolves the name.
A qualified record after `let` can also name an enum variant.
The parser reads the initializer first.
An `else` after the initializer selects the refutable form.

The self-host checker now resolves imported struct patterns against the scrutinee type.
The scoped pretype pass selects the struct desugar after module resolution.
A record-form enum pattern keeps its variant tag test.
The combined pretype scan detects record patterns and other pretype cases in one traversal.

The desugar uses one field-destructure statement per arm.
This statement reports one E021 at the pattern head for an opaque field access.
A pattern that names no field, such as `S { .. }`, remains legal.
A private marker retains the diagnostic text at each bind site.

Value-position matches store their result in a local before the terminal return.
Thus an arm produces the match value and subsequent statements still run.
The module loader also resolves names in preserved record patterns.
The checker can then recover a nested field pattern before it selects the struct desugar.

The regression case exercises both checkers and both interpreters.
It runs on x86-64, ARM64, and Wasm with strict semantic lowering and the allocation census.
It covers guards, field renames, whole-value patterns, dynamic string fields, and nested field patterns.
It also covers exhaustive boolean fields and imported record-form enum patterns with `else`.
Seven opaque-pattern cases each require exactly one E021 from each checker.

The focused suite passes in 30.106 seconds without skipped target legs.
The parser and module-loader suites pass in 5.947 and 1.035 seconds.
The complexity gate passes without a baseline change.
The [validation archive](../benchmarks/qualified-struct-patterns-2026-10-23/) contains the original failures and test logs.
