# Bare enum values beside concrete instances

Issue #12903 reports an oracle failure after an unrelated import.
A recursive generic enum has two concrete instances and a bare nullary variant.
The first checker pass accepts the program.
The monomorphiser removes the generic declaration and leaves the bare type unresolved.

The monomorphiser now gives that bare type a concrete clone with `never` arguments.
The clone preserves variants that do not need a type argument.
Tuple literals also pass each expected element type to the checker.
This lets a bare variant resolve inside an annotated tuple after the rewrite.

The runtime test found a second failure in the self-host drop generator.
It considered fields of an impossible variant when it selected a recursive drop helper.
That helper had no possible next node, and the reference count certifier rejected it.
The child and recursive-tail tests now exclude impossible variants.

Six oracle cases cover locals, arrays, tuples, fields, and return values.
The conformance case combines a bare leaf with string and integer trees.
It checks both payloads and passes the leaf to a function with a bare enum parameter.
