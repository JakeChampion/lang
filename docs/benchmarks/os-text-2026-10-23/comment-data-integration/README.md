# OS text checks after the current main merge

The source commit is `f495b5f54ba22687b88cdb35ba425e0dfd579a32`.
It retains the owner comment cleanup and the capture fixes.
A fresh bootstrap reproduces the earlier OS compiler bytes at every stage.
Stages 2 and 3 have SHA-256 `e7df2c474bf520ee0eb96e4a15cd7c335812ee42fc2568f569d01bb961614d19`.
The manifest retains both bootstrap source commits.

The family suite runs 46 cases across two packages.
All 43 applicable cases pass. Three Darwin executions skip on this Linux host.
The unchanged feature census and complexity limits pass.

The full Go checker, interpreter and source checks run 2400 cases across three packages.
All 2397 applicable cases pass. Three synthetic-AST cases skip.

The matrix passes all 20 commands with 88 native and 72 portable checks.
It covers 13 malformed UTF-8 filename classes and valid multibyte names.
All five runtime allocation counts balance with zero live bytes.
Darwin builds pass.

The predecessor fails the first malformed-name check in both interpreters and native x86-64 output.
The candidate passes the same input.
All four baseline commands record their exact expected status.
The archive retains 50 output logs and source inputs.

All 11 strict driver size checks pass without baseline changes.
All 490 compiler and library Fern files pass the canonical format check.
The changed Go files pass Go format and Go vet.
The clean Go oracle records the exact source revision.
The source manifest records all 497 compiler and library files.
