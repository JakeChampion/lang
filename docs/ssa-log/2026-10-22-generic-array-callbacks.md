# Generic callbacks through array methods

Array methods bind a named generic callback from the receiver and other
arguments. The checker uses the bound parameter types for destination checks
and instance collection. An open method result can take the callback result.
An enclosing function's type variable remains a fixed contract.

The monomorphizer reads the folded array method's signature. It binds the
receiver element and erased method variables before it selects the callback
instance. A residual result variable does not block inference from concrete
callback parameters.

Numeric identities and defaults retain their concrete literal suffixes.
This keeps an i64 or f32 receiver from becoming i32 or f64.

Named interpreter callbacks use the ordinary function call path. This retains
the declaration's generic bounds and binds their runtime type arguments.
Lambda callbacks retain their captured environment.

The Go checker also checks a concrete callback against the generic callee's
contract. It previously skipped that check after callback instantiation.

The conformance case covers map, filter, fold, views, bounded callbacks,
wide integer values, and f32 identities. Differential cases cover unresolved
variables, missing bounds, invalid instances, arity, and wrong results.

This resolves #12977 and #12979.
