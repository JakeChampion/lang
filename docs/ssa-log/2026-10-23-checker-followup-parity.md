# Checker follow-up parity

Source admission now rejects the private memory and syscall primitives with E001.
Generated runtime modules use a separate checker entry that permits their private primitives.
The public `__fern_rc_dec` primitive keeps its current contract.
User declarations with the `__raw_` prefix retain normal function dispatch.

A generic call must infer every type parameter from its arguments or its actual destination.
A return type alone supplies no constraint.
The checks cover unused calls, explicit destinations, return positions, and unbound array results.

Literal match arms retain their source positions through the synthetic equality tests.
A wrong literal pattern now reports E035 at that literal.
An ordinary equality with incompatible types retains E041.

An inferred non-void result now requires a return on every exit path.
The inferred failure edge of `?` contributes to that result.
The missing return reports E052 beside the current E081.

Object-safety diagnostics identify the unsafe method and the first use of the trait object.
Repeated uses do not produce duplicate reports for the same trait.
Use callbacks retain their source positions, callback arity checks, and unknown-type labels.
The checker emits those diagnostics in the same order as the Go oracle.

The new CLI comparison checks complete diagnostic headers for 21 source programs.
It compares codes, positions, messages, multiplicity, and order.
The expanded code and message tables cover positive and negative cases.
The runnable fixture checks a generic result destination and direct and indirect calls to a declared function with the `__raw_` prefix.
Its leak census entry expects no live allocation.

This resolves #12776.

All 21 header comparisons pass, including nested use callbacks and repeated invalid use bindings.
The full code table, message table, bundle differential, and diagnostic sequence tests also pass.
The error corpus has no known exceptions.
The full leak census, formatter corpus, runtime-unit checks, and five execution modes pass.
The source checks, feature census, Go vet, and source format checks pass.

## Integration checks

The diagnostic walk reads the rewritten literal-match chain and retains each pattern's source position.
It handles the temporary binding for a scrutinee that is not an identifier.
Body diagnostics follow statement order, after signature and trait-object validation.
An unresolved use callback retains the enclosing result contract.
Method arguments inherit the receiver's concrete type arguments before a nested generic call is checked.

Internal runtime probes now use `fern.run_runtime` through `drivers/runtime_run.fern`.
This entry selects the runtime checker explicitly and retains the target and sandbox gates.
The public compiler continues to reject private primitives in source programs.

The expanded header comparisons and declaration-order checks pass locally.
The generic-call, native syscall, WebAssembly refusal, and user-world composition regressions pass on the available targets.
The runtime probes cover native execution, WebAssembly execution, and assembly emission for the native targets.
The complete PR run supplies the remaining platform and performance checks.
