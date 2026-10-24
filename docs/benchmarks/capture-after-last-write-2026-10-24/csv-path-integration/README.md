# Capture checks after the supertrait fixes

The source commit is `050683019d3c8e18c48674c8e27e4adb51e7849e`.
It includes the updated record compiler and CSV parser fixes.
The capture implementation retains its earlier source bytes.
The earlier broad and paired measurements retain their original source scopes.

All 41 focused test results pass without failures or skips.
They cover capture order, interpreter bindings, same-name local functions and the repository limits.
All 33 matrix commands pass with the expected output.
Both interpreters agree with all three execution targets.
The nine runtime allocation counts balance with zero live bytes.
Darwin passes its compile checks.

One additional pair checks each of three Map sizes.
At 25000, 50000 and 100000 items, baseline allocations are 50016, 100017 and 200018.
The candidate uses 15, 16 and 17 allocations.
One additional compiler pair checks the entire compiler source.
The candidate output is byte-identical to its final stage 3 compiler.
The report retains the elapsed times, host details and executable hashes.

The released stage0 pin produces byte-identical stages 2 and 3.
Their SHA-256 is `3abcae01dc938975608d1bb34765a9abff968b94146332c37a1a81a72c0cb5e6`.
All 11 strict driver size checks pass without baseline changes.
The changed parser and Go test files pass their format checks.
The manifest records all 497 compiler and library source files.
The archive retains all 66 matrix output logs.
