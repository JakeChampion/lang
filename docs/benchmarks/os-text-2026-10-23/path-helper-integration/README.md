# OS text checks after the supertrait fixes

The source commit is `0e6cb8864dfbf7a63ae1b42ffc5c29bcd1c59b1d`.
It includes the updated capture compiler and CSV parser fixes.
The trait path scan has a separate helper to meet the unchanged complexity limit.
All three compiler stages have identical bytes before and after this extraction.
The OS text implementation retains its earlier source bytes.

All 279 applicable test results pass across two packages.
Two Darwin-only cases skip on this Linux host.
The tests cover OS text, generic supertraits, parser positions and the unchanged repository limits.
All 20 matrix commands pass against 88 native checks and 72 portable checks.
The inputs cover 13 malformed UTF-8 filename classes and valid multibyte names.
All five runtime allocation counts balance with zero live bytes.

The earlier capture compiler and Go reference fail the first malformed-name check.
The current compiler and Go reference pass the same input.
The baseline native executable also fails that check.
Four baseline commands retain their exact output logs.
All three execution targets pass, and Darwin passes its compile checks.

The released stage0 pin produces byte-identical stages 2 and 3.
Their SHA-256 is `e7df2c474bf520ee0eb96e4a15cd7c335812ee42fc2568f569d01bb961614d19`.
All 11 strict driver size checks pass without baseline changes.
The changed parser and Go test files pass their format checks.
Go vet passes for both test packages.

Go production source matches the earlier oracle checkpoint `81fbf50964f8763695b04b8ceeff80dbf56517bc`.
The earlier 2205-case Go sweep and full format checks retain their recorded source scope.
The current manifest records all 497 compiler and library source files.
The archive retains all 50 baseline and matrix inputs and output logs.
