# Lexical source error parity

Non-ASCII letters and digits now get the identifier-specific P001 message.
The lexer uses the Unicode tables generated from the oracle's Unicode version.
Symbols retain the ordinary unexpected-character message.

Entry files and stdin are read as bytes before source decoding.
Valid UTF-8 becomes parser input. Invalid bytes produce a positioned P001.
The decoder scans the whole source, including comments and literals.
An invalid byte takes precedence over an earlier token error, as in the oracle.

Positions count byte columns and preserve line breaks.
The temporary unchecked text never escapes after a failed UTF-8 scan.

The strict decoder rejects overlong forms, surrogates, oversized scalars,
truncated sequences, continuation bytes, and invalid leading bytes.
The source reader shares this behavior across check, interpreter, compile,
format, and stdin paths. File system failures retain their read-error report.

The differential table covers letters, digits, symbols, and malformed UTF-8.
Mode tests compare the same diagnostic across files, stdin, and three compile targets.
The lexer assertions pass on x86-64, ARM64, and Wasm.
The stdin, unreadable-module, and parse-order regressions also pass.
The source checks, Go lexer tests, feature census, Go vet, and formatting pass.

This resolves #12749.
