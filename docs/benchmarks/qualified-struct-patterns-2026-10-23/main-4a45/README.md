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

Stages two and three match at 14,248,048 bytes.
Their SHA-256 is ddc43d9e48eff314e03907680e7f4de18c10eae865ad526a6f8074ec35af7337.
All eleven strict size rows pass without baseline changes.
The report uses the measured bootstrap row and ten stock-driver builds.
Those builds pass in 156.460 seconds.

The broader checker, pattern, value-block, and semantic-execution suite passes in 170.691 seconds.
The earlier archives retain their completed proofs and source scopes.
