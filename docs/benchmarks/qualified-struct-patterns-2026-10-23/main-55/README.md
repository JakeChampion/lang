# Validation with the import diagnostics update

This record covers main `55e8bdc33` and source commit `2c1c99d56`.
The import diagnostics update merged without source conflicts.

A fresh compiler built by the pinned stage 0 and a fresh Go oracle pass
all 24 qualified-pattern feature checks. These include both checkers, both
interpreters, all three compiled targets, and seven opaque-field cases in
both checkers. Native and core WebAssembly allocation censuses balance.
The selected module loader and Go parser tests also pass.

The current CI run supplies the complete bootstrap and driver size gates.
The earlier local bootstrap records retain their explicit input scope.
