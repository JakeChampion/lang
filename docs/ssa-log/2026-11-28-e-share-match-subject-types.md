# Share checked match subject types in diagnostics

Part of #11534 under #8171.

Call diagnostics know the subject type before they check the arms. The checker now uses that type to bind variant payloads. Assignment diagnostics also check the subject once. Each arm gets its own scope from this type. The checker visits the arm bodies first, then lambdas in the subject and guards.

## Compile costs

Both stage-2 compilers read the same source and standard library from main `c634e0e9a91f`. Callgrind counted x86-64 Linux instructions with the verifier enabled and identical command lines. Both compilers emitted identical binaries for the full checker and each numbered fixture.

| Workload | Main | Branch | Change |
| --- | ---: | ---: | ---: |
| Full checker | 97,397,824,941 | 97,388,906,517 | -8,918,424 |
| Check only | 15,074,233,144 | 15,065,317,277 | -8,915,867 |
| 1 variant arm | 19,047,994 | 19,044,812 | -3,182 |
| 8 variant arms | 21,035,858 | 21,010,304 | -25,554 |
| 32 variant arms | 28,111,643 | 28,009,385 | -102,258 |
| 64 variant arms | 38,683,114 | 38,478,584 | -204,530 |
| scalar | 17,792,389 | 17,792,385 | -4 |
| 32 invalid arm bodies, check only | 8,796,512 | 8,695,881 | -100,631 |

The full checker compile allocated and freed 75,027,245 objects on main and 75,023,858 on the branch. Both ended with zero live bytes. The stage-2 executable changed from 22,142,208 to 22,144,192 bytes.

## Verification

Both stage-2 executables reproduced themselves at stage 3. The two full runs of `scripts/selfhost-emit-hashes` matched. All 6,318 outcomes matched, including 4,479 binaries and 1,839 refusals. The report SHA-256 was `df6a28349a2f65b2be2050748dea4c97b22f0a793ef0725af1e8ce714045931d`.

All 537 expected-error JSON diagnostics matched, along with six fixed checker binaries, the HTTP component and two freestanding assembly outputs. The focused match and pattern programs passed 552 paired target checks on x86-64 Linux, ARM64 Linux and WASI.

A generic enum program calls a lambda in the subject. A guard lambda captures an arm variable that shadows an outer local. Both builds passed on all three targets in ordinary, sanitizer and leak-check modes. The leak checks allocated and freed two objects. The negative case has independent errors in the subject lambda, guard lambda and arm body. Both checker drivers and both CLIs returned the same three E003 messages in the same order.

The changed checker passed the formatter and both compiler lint ratchets. The feature comparison used the prebuilt Go test executable.

Nine repository programs exercise guards, alternatives and generic error payloads. They passed 54 ordinary and sanitizer comparisons on the three targets. Both builds passed all 115 certifier driver cases.

Both builds had 739 unproved ownership operations in the native driver and 71 in the WASI checker. The branch refuted 5,387 planted imbalances and weakened 230, compared with 5,386 and 229 on main. Both missed 229. The new match helper refuted three cases. The lambda helper refuted one fewer, and one case in assignment diagnostics changed from refuted to weakened. The other 2,377 shared function records matched.

On the main checker source both stage-1 compilers counted 57,942,596 bytes in 1,006,767 shared-array copies. Both counted 57,939,004 bytes in 1,006,765 copies on the branch source. All five ownership measurements and both array-copy measurements passed the strict comparator on their respective sources.

## Reproduce the match workload

Use N variants, where N is 1, 8, 32 or 64. Give each variant one integer payload. Have `choose(x)` return the last variant with payload x.

In `value(x)`, match on `choose(x)`. Arm i binds v and returns v + i. Return −1 after the match. Run `value(17)` in main and subtract 16 + N.

The scalar control returns zero. For the diagnostic workload, give each of the 32 arms an integer local initialized with true. Both checkers reported all 32 E003 errors.
