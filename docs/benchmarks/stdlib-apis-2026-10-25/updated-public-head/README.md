# Updated public branch validation

Source: 798ef6e5d214da75cc8f928c5b77ecd39c7910cb.
Main base: 691b32567f9d21472248fe6c4e44c1197de210ef.
Public parent: cc5ca94757fbd90cae2221a991aac37ecfba5b14.

The public branch was updated with current main.
The batch preserves that update and the upstream fdlibm implementation.
Issue 13182 is closed upstream.
The new boundary fixture remains as regression coverage.

All 16 hyperbolic matrix commands pass.
Both interpreters and three runtime targets produce the expected results.
All three runtime allocation censuses balance.
The rebuilt embedded compiler passes all 136 commands.

A fresh official bootstrap produces the same final compiler.
Stage 2 and stage 3 match at 14,570,816 bytes.
The source certificate identifies the current inputs.
The formatter, gofmt, and vet checks pass.
Earlier capsules retain the full regression and size evidence.
