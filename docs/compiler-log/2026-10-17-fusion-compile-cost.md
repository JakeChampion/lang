# Keeping the compiler's fusion helpers separate

The fusion pass's context construction, chain search, rewrite and remaining-site
report are separate operations. Inlining them into `fuse_pipelines` makes a large
control-flow graph that the ownership certifier must walk under each reuse-token
assumption. Keep these four compiler-internal boundaries with `@noinline`:
`context`, `find_chain`, `fuse` and `remaining_sites`.

This changes no global inlining rule or fusion admission rule.

## Cold whole-compiler measurement

The control is main at `1a91fc49aae61ea1d0091e364ae2fe98ea47bd25`. Both
compilers were built by that revision's stage 2, then compiled their respective
`compiler/fern.fern` and matching stdlib for x86-64 Linux. Valgrind 3.24
Callgrind counted guest instructions. `FERN_RC_CERTIFY_MEMO=0` disables the
certifier's persistent memo; each measurement starts a new compiler process.

| Certification | Control instructions | Four boundaries | Change |
| --- | ---: | ---: | ---: |
| `FERN_RC_CERTIFY=1` | 127,342,044,044 | 123,371,420,697 | -3.12% |
| `FERN_RC_CERTIFY=0` | 70,582,813,122 | 70,499,277,866 | -0.12% |

Enabling certification produces byte-identical output to disabling it within
each variant. The control output is 12,289,104 bytes, SHA-256
`af2931836bc31181dc1683e44d4d08a9d26cf73001addc79d8da949d9420e4d2`;
the candidate is 12,287,136 bytes, SHA-256
`cb0e03410f07c5567d735514f8161101b01b2a6e1f75c2dda7a9394fd7810dcd`.

The difference between enabled and disabled runs is 44.57% of the control's
enabled count and 42.86% of the candidate's. This improves #12181's cost, but
does not meet its roughly 15% target. These are instruction counts, not native
wall-time measurements.

## Correctness

With the scope reuse below included, the control and candidate emit byte-identical
executables for all 35 frozen performance programs and `compiler/checker.fern`
on x86-64, ARM64 and WASI: 108 comparisons. Native benchmark output and exit
status also agree. The fusion execution, allocation, effect-order and reporting
tests, including the ARM64 and WASI legs, and the unchanged complexity gate pass.

## Reusing the checker's initial scopes

The view-summary and diagnostic loops in `check_module_pass` built each
function's initial scope with identical inputs. Diagnostics now read the initial
scope from `slc_scopes`, which the view-summary loop already populated. Each
diagnostic pass continues to thread its own local bindings.

Measured independently against the same control, compiling its frozen
`compiler/checker.fern` takes 24,642,259,751 instructions instead of
24,650,405,816, a 0.033% reduction. The emitted checker executable is
byte-identical. Checker diagnostic differentials, view-escape tests and the
unchanged complexity gate pass, and bootstrap stages 2 and 3 agree. This removes
a duplicate scope build; it does not supply the per-node fact tables proposed in
#11534 or complete the walk consolidation in #11303.

The combined branch reaches a bootstrap fixed point at 12,287,200 bytes,
SHA-256 `5a06fc6a29dc276a7e1a2ccdd7757776cd29d4d2f4d1e4f4c49015981add678f`.
The two instruction measurements above isolate their respective changes; they
are not a measurement of their combination.
