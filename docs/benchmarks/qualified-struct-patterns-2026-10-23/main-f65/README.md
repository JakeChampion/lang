# Main integration validation

Base: `f65b701a1ca61145ed6a3e521c5b92d1b846932b`.

A fresh compiler built by the locked stage0 and a fresh Go oracle pass all 24 feature checks.
The checks cover both checkers, both interpreters, three compiled targets, and opaque field refusals.
Native and core WebAssembly allocation censuses balance.
The selected Go checker, module loader, and parser tests pass.

The source digest covers every tracked compiler and standard library file, in sorted path order.
Each path and its contents have a zero byte after them.
The validation record gives the source commit and both executable digests.
CI supplies complete bootstrap and driver size gates for this integration.
Earlier measurements retain their recorded input scope.
