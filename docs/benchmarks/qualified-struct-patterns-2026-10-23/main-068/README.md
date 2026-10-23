# Qualified patterns after the collection API merge

The branch includes main at `0681f37a0`.
All 24 feature checks pass with compilers built from this source.
They cover both checkers and interpreters, three compiled targets, and opaque-pattern rejections.
The compiled runs use sanitizer, strict lowering, and leak checks.
The parser, checker, and module-loader Go packages also pass.
The identity file records source and executable hashes.

The merged compiler and library source at `ea33ce1d1` has the same source digest.
