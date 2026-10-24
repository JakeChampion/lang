# Control values and grammar parity

The source commit is `e195d3b14115941fd89583206eb72e50a6f5c4b2`.
The manifest and Go oracle version record the full source identity.
Statement match arms accept expressions or blocks with optional commas.
Pattern conditions can produce values through `if let` expressions.
The condition runs once and its bindings stay within the successful branch.

Both parsers require braces around loop bodies.
The grammar covers opaque structs and resource declarations.
It also records field visibility, pattern bindings and value block items.
The specification removes stale corpus counts.
The language guide explains the explicit loop and pattern-condition form instead of `while let`.

Missing required block values report E061.
The Go checker returns an unknown type after this error.
This prevents an additional branch-type error caused by the missing value.
Six regression cases retain E031 for actual type mismatches and actual void tail expressions.

All 563 compiler follow-up test results pass without failures or skips.
The six Go packages pass 3223 test results.
Eleven synthetic-AST cases intentionally skip.
All 190 matrix commands pass with identical expected output.
They check both interpreters, both formatters and four build targets.
All 30 runtime allocation counts balance with zero live bytes.

All 54 diagnostic checks have the same code sets in both compilers.
The released stage0 pin produces byte-identical stages 2 and 3.
Their SHA-256 is `489e47e90393b87a746fd6d685f778cd258d65a3fa85e974eaa139e3e59841b4`.
All 11 strict driver size checks pass without baseline changes.
Go vet and the changed-file format checks pass.

The earlier broad scope passes 3933 compiler cases and all 1201 canonical Fern format checks.
Its source manifest and native build identity remain separate from this follow-up.
The current manifest records 497 native source files and 4315 Go files.
The archive retains all 535 matrix and diagnostic inputs and output logs.
