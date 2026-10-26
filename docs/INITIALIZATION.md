# Initialization and optional values

Status: [policy] normative for ordinary typed Fern values.

A local scalar or array declaration requires an initializer. The binding becomes
available after the initializer passes the checker. A binding cannot read itself
during initialization. A prior binding with the same name follows the normal
shadow rules.

Each branch and loop body has its own scope. A declaration inside that scope does
not become available after the body. An assignment to an initialized outer
binding updates that binding. Both branches can assign the same outer binding.

A struct literal must supply each required field. An omitted field does not
acquire an implicit zero value. Composite updates can retain fields from a valid
base value under the normal struct update rules.

Fern has no general null literal. The name `null` follows ordinary identifier
resolution. Absence uses an optional value such as `Option[T]`. Code must select
a payload through a checked operation, such as a match on `Some` and `None`.
It cannot read a payload field directly from the optional value.

These rules do not prove initialization of raw or foreign memory. Raw memory can
bypass the typed value representation and its checks. The explicit unsafe
boundary, foreign ownership, and raw allocation contracts require separate work.
These rules do not permit unrestricted shared-heap threads.

The [semantic index](../spec/semantics.md) links each rule to its conformance case.
The cases cover missing initializers, self-reference, scope escape, missing
fields, invalid optional access, and valid branch and optional controls.
