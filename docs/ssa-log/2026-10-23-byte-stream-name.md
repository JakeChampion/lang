# Byte reader and async channel names

Issues: #12708, #13068, and #13069.

The buffered byte reader is `ByteStream`.
The generic async channel remains `Stream[T]`.
Both frontends declare the same byte-reader fields and method types.
The standard library and its callers use the byte-reader name.
The lexical bridge for the old bootstrap pin is removed.
The new stage0 pin knows that bridge and can build the updated source.

The formatter also retains the checked cast operator `as?`.
It applies cast precedence and parentheses to both cast forms.
A checked cast no longer prints as an invalid prefix operator.
The format regressions cover numeric casts, nested casts, and casts of trait objects.

The Go formatter retains a tuple or record binding's inline comment.
It also finds the initializer's final line for a comment after a split statement.

The runtime case combines the byte reader and the generic channel declaration.
It also uses `default` as a function name and checks integer conversion bounds.
It runs on x86-64, ARM64, and WASI with sanitizer and leak checks.

Validation records are in `docs/benchmarks/byte-stream-name-2026-10-23/`.
