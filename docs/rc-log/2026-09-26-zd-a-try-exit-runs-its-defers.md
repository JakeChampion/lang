# A `?` exit runs its defers on the typed path

The typed path compiled a `?` failure exit as a bare return: the pending
defers and errdefers never ran (#10322). No refusal named it, so the default
CLI shipped the wrong behaviour, and strict mode did not catch it.

The parser's defer desugar rewrites every `return` to run the cleanup first,
but `?` is lowered after it and cannot be rewritten there. So the desugar
leaves the cleanup a `?` owes behind two always-false guards at the top of
the body, `if (__dfa_tryall) {…}` and `if (__dfa_tryerr) {…}` (#4334). The AST
lowering's `lower_try` replays those at the failure edge. The typed path read
them as dead code. `semsource` now collects the two blocks when it starts a
body and lowers them on the failure edge of each `?`, plain defers first.

`TestSelfHostTryDefer` pins four cases on x86-64, arm64 and wasm, each value
checked against the native interpreter and native x86-64. Without the fix,
the three failure-path cases fail on every target. `TestSelfHostDeferBlockLocalIR`
moves to the CLI with this change; its two `?` cases were the ones that
found the bug.
