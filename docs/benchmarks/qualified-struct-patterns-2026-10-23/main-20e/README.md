# Main integration validation

Base: `20e3c13059bde8f52e0ba8dc8ba927ccf8d4071a`.

A fresh compiler built by the locked stage0 and a fresh Go oracle pass all 24 feature checks.
The checks cover both checkers, both interpreters, three compiled targets, and opaque field refusals.
Native and core WebAssembly allocation censuses balance.
The selected Go checker, module loader, and parser tests pass.

The source digest covers every tracked compiler and standard library file, in sorted path order.
Each path and its contents have a zero byte after them.
The validation record gives the source commit and both executable digests.
CI supplies complete bootstrap and driver size gates for this integration.
Earlier measurements retain their recorded input scope.
