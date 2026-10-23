# Integration with main 4a45ac1

Base: 4a45ac12ff20c413ff997e874c26bc92fb2371bf.
Source: 7a2457afc2c64a357263b571a18eb301692d57ed.

The selected self-host suite passes in 109.018 seconds.
Both checkers, both interpreters, and all three runtime targets pass.
Exact diagnostic text, parse-error order, malformed declarations, consuming calls, and lane operations also pass.
The compiler and stdlib complexity gates pass without a baseline change.

The Go parser, module loader, and complete Go checker suites pass.
Their times are 8.285, 1.542, and 8.213 seconds.
The merge retains main's tuple-arity rewrite and tuple-literal diagnostic recovery.
The combined pretype scan detects those tuple rewrites and unresolved record patterns in one traversal.

The parser shares one local-struct classifier across its three callers.
Separate statement handlers retain the previous bodies for conditionals, returns, loop exits, and expression statements.
This keeps the complexity gate unchanged after the frontend integration.

Bootstrap and strict driver-size checks are in progress for this source.
The earlier archives retain their completed proofs and source scopes.
