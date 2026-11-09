# Ownership state for local shadow bindings

A WavPack reader exposed a false E050 diagnostic in both checkers.
An inner local reused the name of a consumed owned parameter.
The ownership pass applied the move state of the parameter to the fresh local.
The prior compiler and the Go oracle both reject the reduced control.

Both checkers now evaluate the initializer before the local hides the outer binding.
They restore the outer ownership state at the end of the lexical scope.
The self-hosted checker reuses the scope rules for pattern bindings.
The Go checker reuses its owned binding scope helper.
Ordinary local declarations do not allocate shadow scope records.

A shared fixture contains seven valid cases and seven invalid cases.
It covers owned and borrowed parameters, initializers, deferred moves, field moves, branches, and loops.
The invalid cases preserve the outer move state and reject real use after move.
The full Go checker package passes.
The native checker and the WASI checker driver pass all 14 cases.

All seven valid cases pass native execution with sanitizers and WASI execution.
Every native case reports zero live bytes.
All seven compile as Android ARM64 shared libraries.
The prior shadow initializer controls and the repository complexity ratchet also pass.

The supported compiler build passes.
Its core SHA-256 is `4352e6887e523028a6ca39b9c238dd51e34bc75c409ad2b086e5d8dbaba954ec`.
The WavPack source reader now passes the type check without a source workaround.
Full CI remains required.

Issue: #14781. Parent: #14509.

The merge with the later loop checks adds five shared controls.
The fixture now has nine valid cases and ten invalid cases.
Local scope records now settle hidden outer moves on both break and continue paths.
The Go checker also places local shadow scopes on the jump scope stack.
The controls reject initializer moves that a jump previously hid and accept restoration before a continue.
