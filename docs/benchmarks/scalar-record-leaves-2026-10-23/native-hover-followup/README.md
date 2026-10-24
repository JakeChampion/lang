# Native playground hover documentation

The source commit is `1ce3fe349119092beecb1cf4e3a3c568a829650e`.
The `std/i32.to_string` declaration again supplies its method description to the language server.
The exact description is part of the native playground contract.
The source change restores one comment line.
The implementation tokens remain unchanged.

All 18 native playground test cases pass without failures or skips.
The hover case checks the complete text, source range, completion entry and definition location.
All 18 browser shim test cases also pass without failures or skips.
The source file passes the canonical format check.

All three fresh compiler stages match the prior public head byte for byte.
Stages 2 and 3 retain SHA-256 `d294ee4d34be58c3f2cd8335c0ba382d39f4732ed99bef46624554038044fb24`.
The archive retains the source patch and full test output.
