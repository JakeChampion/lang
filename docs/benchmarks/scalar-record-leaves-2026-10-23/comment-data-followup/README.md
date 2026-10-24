# Required comment data

The `std/i32.abs` declaration again supplies a concise description for language-server hover help.
The existing import fixture also has its required boundary marker.
Both changes restore comment data consumed by the tools.
The implementation tokens remain unchanged.

All 18 browser shim test results pass without failures or skips.
The existing language-server test checks the signature and the absolute-value description.
The existing fixture test checks both import groups.
The two source files pass the canonical format check.

All three fresh compiler stages are byte-identical to the public base.
Stages 2 and 3 retain SHA-256 `d294ee4d34be58c3f2cd8335c0ba382d39f4732ed99bef46624554038044fb24`.
The manifest records the source commit, source patch and complete test output.
