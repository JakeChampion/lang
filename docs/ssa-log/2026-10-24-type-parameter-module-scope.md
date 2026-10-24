# Type parameter module scope

Issue #13124 shows an imported trait parameter captured by a same-name module declaration.
Both module loaders now carry the type parameters for each declaration.
Type references and associated-call qualifiers keep those bindings.
The outer scope is restored after each declaration.

The self parser also rejects parent traits with array, tuple, function or view syntax.
Generic arguments can still contain these type forms.
The focused cases check module aliases, parent substitutions, default methods and concrete types after generic declarations.
The current integration evidence records both compiler results and runtime allocation counts.

Type parameters do not hide constants or functions in the value namespace.
The Go constant fold also visits trait default bodies before it removes constant declarations (#13126).
It preserves the declaration module and local bindings for those bodies.
