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
