# Byte stream and checked cast integration

The measured source is `7fc8e3b0cfd1f4ffe3bd7288e6bd87b63ffa5e15`.
The packaged source is `7f467b722582594295c3dba4b40824d33b4e476c`.
Only dependency evidence changes between these commits.
Compiler, library, Go code, and test input bytes retain their measured scope.

The byte reader is `ByteStream`. The asynchronous channel remains `Stream[T]`.
The reader retains its `Option[u8]` byte result.
Checked casts use infix `as?` syntax with the correct precedence.

All 125 selected native regression cases pass without skips.
The selected Go suites pass 3,367 cases across seven packages.
Eleven synthetic AST and interpreter cases skip by design.
Both new byte stream checker cases pass in a separate run.

Four fixtures pass 76 commands with both checkers, interpreters, and formatters.
They also build for both Linux targets, WASI, and Darwin.
All 12 runtime allocation counts balance with zero live bytes.
Both formatters agree, and another format pass preserves the result.
Darwin images are built but are not executed locally.

The published stage0 pin produces identical stages 2 and 3 at 14,445,264 bytes.
Their SHA-256 is `849c6a94c4975be9f2d10f6595f3b362b1fb379e8bf2e0d157ac29968408c908`.
All 11 strict size gates pass without baseline changes.
The unchanged feature census and complexity limits pass.
Go vet, Go format, and compiler and library format checks pass.

The capsule records 498 compiler and library files and the exact source scopes.
The consumer certificate checks 12 files against their dependency source.
Those consumers only rename bare `Stream` identifiers. The reader fixture also uses the canonical format.
The source patch excludes benchmark evidence files.
