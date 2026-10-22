# Concrete contracts for generic callbacks

This follows #13026, which supplies generic array callbacks, typed numeric
identities, interpreter generic bindings, and lane support.

The checker collects callback instances through method destinations. This
checks an invalid body at its concrete instance. An unresolved callback gets
E040 without a second mismatch from its abstract signature.

An open method result can take the callback result. A type variable of the
function that contains the call remains a fixed contract.

The Go checker also checks a concrete callback against the generic callee's
contract. It previously inferred from that callback without checking a
mismatch. The callback result also determines an open callee result.

The conformance case covers map, filter, fold, views, bounded callbacks,
wide integer values, and f32 identities. Differential cases cover unresolved
variables, missing bounds, invalid instances, arity, and wrong results.

The complete tree resolves #12977 and #12979.
